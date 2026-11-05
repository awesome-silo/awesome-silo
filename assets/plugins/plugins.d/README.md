# `plugins.d`

Each file here holds plugins that their authors maintain themselves. Authors keep their own entries up to date (description, links, tier requests) without touching the shared [`plugins.yaml`](../plugins.yaml).

When `PLUGINS.md` is generated, these files are merged with `plugins.yaml`. Categories are defined only in `plugins.yaml`, but each plugin here can use any of them.

## Adding your plugin

You can add your own file here, or add your plugin to `plugins.yaml` instead if you'd rather not maintain it yourself. Name the file after your plugin or project (`my-plugin.yaml`).

```yaml
# yaml-language-server: $schema=../schema.json
owners:
  - my-github-handle
  - co-maintainer

plugins:
  - name: "My Plugin"
    description: "One sentence about what it does."
    category: playback
    tier: community
    url: https://github.com/me/my-plugin
    oss: https://github.com/me/my-plugin
```

`owners` lists the GitHub handles of the people who maintain the file. Changes to a file by one of its owners are reviewed lightly; changes by anyone else get a normal review.

## Publishing your own catalog (manifest)

If your plugin publishes a Silo catalog (an `index.json` with the same format as the official and community manifests), point to it instead of describing the plugin by hand. The name, description and link then stay up to date automatically.

```yaml
# yaml-language-server: $schema=../schema.json
owners:
  - my-github-handle

manifests:
  - id: my-plugin          # unique across all files; snapshot is stored as manifests/my-plugin.json
    tier: other            # the only tier allowed here
    category: metadata     # used when none of the plugin's capabilities map to a category
    url: https://raw.githubusercontent.com/me/my-plugin/main/index.json
```

- `tier` must be `other`. A file here cannot claim `official` or `community`.
- A plugin whose ID is already provided by the official or community manifest (or an earlier file) is skipped, so a catalog can't replace another plugin's entry.
- Every plugin in your catalog is listed, with its repository link taken from the manifest. `repo_url` values must be `https`.
- After adding the file, run `go run ./cmd/generate-plugins -update-manifests` and then `go run ./cmd/generate-plugins`, and commit the new snapshot in `assets/plugins/manifests/` with `PLUGINS.md`. The daily refresh keeps it current afterwards; if your URL stops working, the last snapshot is kept.
- You can use `manifests` and `plugins` in the same file.

## Tiers

| `tier`      | Meaning                                                                          |
|-------------|----------------------------------------------------------------------------------|
| `official`  | Maintained by the Silo project. Set automatically for plugins in the official manifest; don't set it here. |
| `community` | Maintained by its own author or community and widely used or endorsed.           |
| `other`     | Everything else, including new or niche plugins.                                 |

Approved community plugins are also read from the community manifest automatically; list a plugin here only if it isn't in a manifest. Set `community` or `other` yourself; maintainers will adjust it in review if needed. Pull requests that set `official` on a plugin the Silo project doesn't maintain will be asked to change it.
