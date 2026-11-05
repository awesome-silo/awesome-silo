#!/usr/bin/env python3
"""Refresh the generated Related section in README.md from related.txt."""

import argparse
import json
import os
from pathlib import Path
import re
import sys
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "related.txt"
README = ROOT / "README.md"
START_MARKER = "<!-- related-projects:start -->"
END_MARKER = "<!-- related-projects:end -->"


def parse_sources(text):
    sources = []
    seen_urls = set()

    for line_number, raw_line in enumerate(text.splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue

        url, separator, display_name = line.partition("|")
        url = url.strip().rstrip("/")
        display_name = display_name.strip()
        if not separator or not url or not display_name:
            raise ValueError(
                f"{SOURCE.name}:{line_number}: expected "
                "'https://github.com/owner/repository | Display name'"
            )

        match = re.fullmatch(r"https://github\.com/([^/]+)/([^/]+)", url)
        if not match:
            raise ValueError(f"{SOURCE.name}:{line_number}: invalid GitHub repository URL: {url}")
        if url.casefold() in seen_urls:
            raise ValueError(f"{SOURCE.name}:{line_number}: duplicate repository URL: {url}")
        if any(char in display_name for char in "\r\n[]"):
            raise ValueError(f"{SOURCE.name}:{line_number}: invalid display name")

        seen_urls.add(url.casefold())
        sources.append((url, match.group(1), match.group(2), display_name))

    return sources


def fetch_repository(owner, repository):
    api_url = f"https://api.github.com/repos/{owner}/{repository}"
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "awesome-silo-related-updater",
    }
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        headers["Authorization"] = f"Bearer {token}"

    request = Request(api_url, headers=headers)
    try:
        with urlopen(request, timeout=30) as response:
            data = json.load(response)
    except HTTPError as error:
        raise RuntimeError(f"GitHub API returned HTTP {error.code} for {owner}/{repository}") from error
    except URLError as error:
        raise RuntimeError(f"Could not retrieve {owner}/{repository}: {error.reason}") from error
    except json.JSONDecodeError as error:
        raise RuntimeError(f"GitHub API returned invalid JSON for {owner}/{repository}") from error

    description = data.get("description")
    if description is None:
        description = "No GitHub description provided."
    description = " ".join(description.split())
    return description


def escape_markdown(text):
    return "".join("\\" + char if char in r"\`*_[]" else char for char in text)


def render_entries(sources, descriptions):
    entries = [
        f"- [{escape_markdown(display_name)}]({url}) - {escape_markdown(descriptions[url])}"
        for url, _owner, _repository, display_name in sources
    ]
    return "\n".join(sorted(entries, key=str.casefold))


def replace_generated_section(readme, entries):
    if readme.count(START_MARKER) != 1 or readme.count(END_MARKER) != 1:
        raise ValueError("README.md must contain exactly one related-projects start and end marker")

    start = readme.index(START_MARKER)
    end = readme.index(END_MARKER)
    if end < start:
        raise ValueError("README.md related-projects markers are out of order")

    content_start = start + len(START_MARKER)
    return f"{readme[:content_start]}\n{entries}\n{readme[end:]}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail if README.md does not match the generated related-project list",
    )
    args = parser.parse_args()

    try:
        sources = parse_sources(SOURCE.read_text(encoding="utf-8"))
        descriptions = {
            url: fetch_repository(owner, repository)
            for url, owner, repository, _display_name in sources
        }
        generated = replace_generated_section(
            README.read_text(encoding="utf-8"),
            render_entries(sources, descriptions),
        )
        if args.check:
            if generated != README.read_text(encoding="utf-8"):
                raise RuntimeError("README.md Related section is out of date; run scripts/update_related.py")
            print("README.md Related section matches related.txt and GitHub descriptions.")
        else:
            README.write_text(generated, encoding="utf-8")
            print("Updated the generated Related section in README.md.")
    except (OSError, ValueError, RuntimeError) as error:
        print(f"Error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
