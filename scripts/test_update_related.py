import unittest

from update_related import parse_sources, render_entries, replace_generated_section


class RelatedUpdaterTests(unittest.TestCase):
    def test_parse_sources_accepts_comments_and_rejects_duplicate_urls(self):
        sources = parse_sources(
            "# source list\n"
            "https://github.com/example/project | Example Project\n"
        )
        self.assertEqual(
            sources,
            [("https://github.com/example/project", "example", "project", "Example Project")],
        )

        with self.assertRaisesRegex(ValueError, "duplicate repository URL"):
            parse_sources(
                "https://github.com/example/project | Example\n"
                "https://github.com/EXAMPLE/PROJECT | Duplicate\n"
            )

    def test_render_entries_sorts_and_escapes_markdown(self):
        sources = [
            ("https://github.com/example/z", "example", "z", "Z [project]"),
            ("https://github.com/example/a", "example", "a", "A project"),
        ]
        entries = render_entries(sources, {
            "https://github.com/example/z": "Description | one",
            "https://github.com/example/a": "Another project",
        })
        self.assertEqual(
            entries.splitlines(),
            [
                "- [A project](https://github.com/example/a) - Another project",
                "- [Z \\[project\\]](https://github.com/example/z) - Description | one",
            ],
        )

    def test_replace_generated_section_preserves_surrounding_content(self):
        readme = f"Before\n{START}old\n{END}\nAfter\n"
        self.assertEqual(
            replace_generated_section(readme, "new"),
            f"Before\n{START}\nnew\n{END}\nAfter\n",
        )


START = "<!-- related-projects:start -->"
END = "<!-- related-projects:end -->"


if __name__ == "__main__":
    unittest.main()
