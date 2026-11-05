# Silo Plugin YAML Configuration Format

The local Go generator builds `PLUGINS.md` from `plugins.yaml`, the files in [`plugins.d`](plugins.d/README.md), the [manifest snapshots](#remote-manifests-manifests), and the template [`PLUGINS.md.tmpl`](PLUGINS.md.tmpl). Run it from the repository root:

```sh
go run ./cmd/generate-plugins
```

To verify that the checked-in output matches the source files without changing it:

```sh
go run ./cmd/generate-plugins -check
```

Each YAML file is validated against [`schema.json`](schema.json). The generator also rejects plugins that use an undefined category and duplicate plugin names.

## Blacklisted owners

The generator reads `PLUGIN_BLACKLISTED_OWNERS` as a comma- or newline-separated list of GitHub owners and excludes their plugins across all manifests and plugin files. In CI, provide this value through a repository Actions secret with the same name. To regenerate locally, set the environment variable before running the generator.

## `categories` (plugins.yaml only)

The sections of `PLUGINS.md`, in display order. Categories with no plugins are not shown.

```yaml
categories:
  - key: playback
    display: "▶️ Playback"
```

## `plugins`

| Field         | Required | Description                                                                 |
|---------------|----------|-----------------------------------------------------------------------------|
| `name`        | Yes      | Plugin name. Must be unique across all files.                               |
| `description` | Yes      | One-sentence description.                                                   |
| `category`    | Yes      | A `key` from `categories`.                                                  |
| `tier`        | Yes      | `official`, `community` or `other` (see below).                             |
| `url`         | Yes      | Homepage or repository; the name links here.                                |
| `oss`         | No       | Source repository URL. Sets the OSS column.                                 |
| `beta`        | No       | Shows a ⚒️ badge next to the name.                                          |
| `unsupported`| No       | Places the plugin in a separate unsupported table at the end of `PLUGINS.md`, with a warning that asking for support on an official Silo channel will result in a ban. |

Within each category, plugins are ordered by tier (official, then community, then other) and then by name.

| `tier`      | Meaning                                                              |
|-------------|----------------------------------------------------------------------|
| `official`  | Maintained by the Silo project.                                      |
| `community` | Maintained by its own author or community and widely used or endorsed. |
| `other`     | Everything else.                                                     |

## Remote manifests (`manifests`)

Official and approved community plugins are read from plugin manifests published by the Silo project instead of being listed by hand, so do not add them to `plugins.yaml`.

```yaml
manifests:
  - id: official
    tier: official
    url: https://raw.githubusercontent.com/Silo-Server/silo-plugins/main/manifest.json
  - id: community
    tier: community
    url: https://raw.githubusercontent.com/Silo-Community/silo-plugins/main/manifest.json

capability_categories:
  metadata_provider.v1: metadata
  auth_provider.v1: auth
```

- Every plugin in a manifest gets that manifest's `tier`.
- A plugin's name, summary and repository link come from its `presentation` block (falling back to its first capability's name and description). The repository link is used for both the URL and OSS columns.
- A manifest URL can point to a catalog containing a `plugins` list or directly to one plugin's `manifest.json`. For a standalone manifest, the repository URL comes from its presentation source/homepage URL, or is inferred from its `raw.githubusercontent.com` URL.
- The manifests have no reliable category, so a plugin is placed using the first of its capabilities that appears in `capability_categories`. If a plugin has none, generation fails and names the plugin: add the capability to the mapping (and a category, if needed).
- A `plugins.d` file can declare its own manifests (author-published catalogs), but only with `tier: other`. See the [`plugins.d` README](plugins.d/README.md). Manifest `id`s must be unique across all files.
- A manifest can set an optional `category`, used for a plugin only when none of its capabilities are in `capability_categories`.
- `unsupported_plugins` can list plugin IDs from a catalog that are outside Silo's supported scope. Each is placed in the separate unsupported table with the same warning as a manually listed plugin with `unsupported: true`; every listed ID must exist in the manifest snapshot.
- Manifests are listed in priority order: those in `plugins.yaml` first, then those in `plugins.d` files by file name. If a plugin ID appears in more than one, the first one wins and the rest are skipped. For example, the community manifest repeats the official requests plugins' IDs, so only the official entries are listed.
- A manifest plugin whose name matches another entry is rejected as a duplicate.

The generator never fetches manifests while generating, so `-check` gives the same result everywhere. Instead each manifest is saved as a trimmed snapshot in [`manifests/`](manifests) (`<id>.json`). Refresh them with:

```sh
go run ./cmd/generate-plugins -update-manifests
go run ./cmd/generate-plugins
```

The generate-plugins workflow does this daily and opens a pull request if anything changed. If an `other` manifest can't be fetched, its previous snapshot is kept and a warning is logged; a failure in the official or community manifest fails the refresh. Snapshots no manifest refers to are removed. To test against local copies, use `-update-manifests -manifests-from <dir>`, where `<dir>` holds `<id>.json` upstream manifests.

## Self-managed entries

Authors can maintain their own plugins in a file of their own in [`plugins.d`](plugins.d/README.md). Such a file has a top-level `owners` list of GitHub handles and a `plugins` list with the same shape as above.
