package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	categories := []Category{{Key: "ui", Display: "UI"}}
	tests := []struct {
		name    string
		plugins []Plugin
		want    string
	}{
		{name: "ok", plugins: []Plugin{{Name: "A", Category: "ui"}}},
		{name: "undefined category", plugins: []Plugin{{Name: "A", Category: "nope"}}, want: "undefined category"},
		{name: "duplicate", plugins: []Plugin{{Name: "A", Category: "ui"}, {Name: " a ", Category: "ui"}}, want: "duplicate plugin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validate(categories, test.plugins)
			if test.want == "" && err != nil {
				t.Fatalf("validate() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestFilterBlacklistedOwners(t *testing.T) {
	plugins := []Plugin{
		{Name: "Blocked URL", URL: "https://github.com/owner-one/plugin"},
		{Name: "Blocked source", URL: "https://example.com/plugin", OSS: "https://github.com/owner-two/plugin"},
		{Name: "Allowed", URL: "https://github.com/Silo-Server/plugin"},
	}

	got := filterBlacklistedOwners(plugins, []string{"@OWNER-ONE", "owner-two"})
	if len(got) != 1 || got[0].Name != "Allowed" {
		t.Fatalf("filterBlacklistedOwners() = %#v, want only Allowed", got)
	}
}

func TestRenderOrdersByTierThenName(t *testing.T) {
	categories := []Category{{Key: "ui", Display: "UI"}, {Key: "empty", Display: "Empty"}}
	plugins := []Plugin{
		{Name: "Zeta", Description: "a | b", Category: "ui", Tier: "official", URL: "https://example.com/z"},
		{Name: "Beta", Category: "ui", Tier: "other", URL: "https://example.com/b", OSS: "https://example.com/b"},
		{Name: "Alpha", Category: "ui", Tier: "community", URL: "https://example.com/a", Beta: true},
	}

	got := render(categories, plugins)

	zeta, alpha, beta := strings.Index(got, "[Zeta]"), strings.Index(got, "[Alpha"), strings.Index(got, "[Beta]")
	if !(zeta >= 0 && zeta < alpha && alpha < beta) {
		t.Fatalf("want official, community, other order:\n%s", got)
	}
	if strings.Contains(got, "Empty") {
		t.Fatalf("empty categories should be omitted:\n%s", got)
	}
	if !strings.Contains(got, `a \| b`) {
		t.Fatalf("pipes in descriptions should be escaped:\n%s", got)
	}
}

func TestRenderUnsupportedPluginDisclaimer(t *testing.T) {
	got := render([]Category{{Key: "playback", Display: "Playback"}}, []Plugin{{
		Name:        "IPTV",
		Description: "Live TV channels.",
		Category:    "playback",
		Tier:        "other",
		URL:         "https://example.com/iptv",
		Unsupported: true,
	}})
	for _, want := range []string{
		"## Unsupported Plugins",
		"outside Silo's supported scope",
		"asking for help in any official Silo channel, including Discord, will result in a ban.",
		"⚠️ **UNSUPPORTED by Silo**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("render() output missing disclaimer text %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Playback") {
		t.Fatalf("unsupported plugin should not appear in category tables:\n%s", got)
	}
}

func TestRenderUnsupportedPluginsAtEnd(t *testing.T) {
	got := render(
		[]Category{{Key: "playback", Display: "Playback"}},
		[]Plugin{
			{Name: "Supported", Category: "playback", URL: "https://example.com/supported"},
			{Name: "Unsupported", Category: "playback", URL: "https://example.com/unsupported", Unsupported: true},
		},
	)
	categoryIndex := strings.Index(got, "## Playback")
	unsupportedIndex := strings.Index(got, "## Unsupported Plugins")
	unsupportedRowIndex := strings.Index(got, "[Unsupported]")
	if categoryIndex < 0 || unsupportedIndex < 0 || unsupportedRowIndex < 0 ||
		categoryIndex >= unsupportedIndex || unsupportedIndex >= unsupportedRowIndex {
		t.Fatalf("unsupported table should follow supported category sections:\n%s", got)
	}
	if strings.Contains(got[:unsupportedIndex], "[Unsupported]") {
		t.Fatalf("unsupported plugin should only appear in the final table:\n%s", got)
	}
}

func TestManifestUnsupportedPlugins(t *testing.T) {
	snapshot := &manifestSnapshot{Plugins: []manifestPlugin{{
		ID:           "silo.live-tv",
		Name:         "Live TV",
		Summary:      "IPTV and guide support.",
		RepoURL:      "https://github.com/example/live-tv",
		Capabilities: []string{"http_routes.v1"},
	}}}
	manifest := Manifest{ID: "catalog", Tier: "other", UnsupportedPlugins: []string{"silo.live-tv"}}
	plugins, err := manifestPlugins(snapshot, manifest, map[string]string{"http_routes.v1": "playback"})
	if err != nil {
		t.Fatalf("manifestPlugins() error = %v", err)
	}
	if len(plugins) != 1 || !plugins[0].Unsupported {
		t.Fatalf("manifestPlugins() = %#v, want the configured plugin to be unsupported", plugins)
	}

	manifest.UnsupportedPlugins = []string{"silo.missing"}
	if _, err := manifestPlugins(snapshot, manifest, map[string]string{"http_routes.v1": "playback"}); err == nil || !strings.Contains(err.Error(), "silo.missing") {
		t.Fatalf("manifestPlugins() error = %v, want an unknown unsupported plugin ID error", err)
	}
}

func TestRunMergesAuthorFilesAndChecks(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) string {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	schema, err := filepath.Abs("../../assets/plugins/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	main := write("plugins.yaml", "categories:\n  - key: ui\n    display: UI\nplugins: []\n")
	write("plugins.d/mine.yaml", `owners: [me]
plugins:
  - name: Mine
    description: Mine does things.
    category: ui
    tier: community
    url: https://example.com/mine
`)
	tmpl := write("page.tmpl", "{{ .Body }}\n")
	out := filepath.Join(dir, "PLUGINS.md")

	opts := options{
		pluginsFile:  main,
		pluginsDir:   filepath.Join(dir, "plugins.d"),
		schemaFile:   schema,
		templateFile: tmpl,
		outputFile:   out,
	}
	if err := run(opts); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	opts.check = true
	if err := run(opts); err != nil {
		t.Fatalf("run(check) error = %v", err)
	}
	opts.check = false
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), "[Mine](https://example.com/mine)") {
		t.Fatalf("author file plugin missing from output:\n%s", got)
	}

	write("plugins.d/bad.yaml", "plugins:\n  - name: Bad\n")
	if err := run(opts); err == nil {
		t.Fatal("run() should reject a plugins.d file without owners and required fields")
	}
}

const officialFixture = `{"plugins":[
 {"repo_url":"https://github.com/Silo-Server/p-auth","manifest":{"plugin_id":"silo.auth","capabilities":[
   {"type":"http_routes.v1","display_name":"Assets","description":"Assets."},
   {"type":"auth_provider.v1","display_name":"Auth","description":"Auth cap."}],
  "presentation":{"display_name":"Auth Plugin","summary":"Sign in."}}},
 {"repo_url":"https://github.com/Silo-Server/p-seerr","manifest":{"plugin_id":"silo.seerr","capabilities":[
   {"type":"request_router.v1","display_name":"Seerr","description":"Routes requests."}]}}
]}`

const communityFixture = `{"plugins":[
 {"repo_url":"https://github.com/Silo-Community/p-seerr","manifest":{"plugin_id":"silo.seerr","capabilities":[
   {"type":"request_router.v1","display_name":"Seerr","description":"Community copy."}]}},
 {"repo_url":"https://github.com/Silo-Community/p-scrob","manifest":{"plugin_id":"silo.scrob","capabilities":[
   {"type":"watch_sync_provider.v1","display_name":"Scrob","description":"Sync."}],
  "presentation":{"display_name":"Scrob Watch Provider","summary":"Sync with Scrob."}}}
]}`

func TestManifestsSnapshotAndRun(t *testing.T) {
	dir := t.TempDir()
	upstream := filepath.Join(dir, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"official.json": officialFixture, "community.json": communityFixture} {
		if err := os.WriteFile(filepath.Join(upstream, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := filepath.Abs("../../assets/plugins/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "plugins.yaml")
	config := `categories:
  - key: auth
    display: Auth
  - key: integration
    display: Integration
manifests:
  - id: official
    tier: official
    url: https://example.com/official.json
  - id: community
    tier: community
    url: https://example.com/community.json
capability_categories:
  auth_provider.v1: auth
  request_router.v1: integration
  watch_sync_provider.v1: integration
plugins: []
`
	if err := os.WriteFile(main, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	tmpl := filepath.Join(dir, "page.tmpl")
	if err := os.WriteFile(tmpl, []byte("{{ .Body }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := options{
		pluginsFile:  main,
		pluginsDir:   filepath.Join(dir, "plugins.d"),
		schemaFile:   schema,
		templateFile: tmpl,
		outputFile:   filepath.Join(dir, "PLUGINS.md"),
		manifestsDir: filepath.Join(dir, "manifests"),
	}

	if err := run(opts); err == nil || !strings.Contains(err.Error(), "-update-manifests") {
		t.Fatalf("run() without snapshots error = %v, want hint to run -update-manifests", err)
	}

	opts.updateManifest, opts.manifestFrom = true, upstream
	if err := run(opts); err != nil {
		t.Fatalf("run(-update-manifests) error = %v", err)
	}
	opts.updateManifest, opts.manifestFrom = false, ""
	if err := run(opts); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	got, _ := os.ReadFile(opts.outputFile)
	for _, want := range []string{
		"[Auth Plugin](https://github.com/Silo-Server/p-auth) | 🔹 Official | ✅ | Sign in. |",
		"[Seerr](https://github.com/Silo-Server/p-seerr) | 🔹 Official | ✅ | Routes requests. |",
		"[Scrob Watch Provider](https://github.com/Silo-Community/p-scrob) | 👥 Community | ✅ | Sync with Scrob. |",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "Silo-Community/p-seerr") {
		t.Fatalf("community copy of an official plugin ID should be skipped:\n%s", got)
	}
}

func TestManifestPluginsRejectsUnmappedCapabilities(t *testing.T) {
	snapshot := &manifestSnapshot{Plugins: []manifestPlugin{{ID: "silo.x", Capabilities: []string{"new_thing.v1"}}}}
	_, err := manifestPlugins(snapshot, Manifest{ID: "official", Tier: "official"}, map[string]string{"auth_provider.v1": "auth"})
	if err == nil || !strings.Contains(err.Error(), "silo.x") {
		t.Fatalf("manifestPlugins() error = %v, want unmapped capability error naming the plugin", err)
	}
}

const authorFixture = `{"plugins":[
 {"repo_url":"https://github.com/someone/p-art","manifest":{"plugin_id":"someone.art","capabilities":[
   {"type":"brand_new.v1","display_name":"Art","description":"Artwork."}]}},
 {"repo_url":"https://github.com/someone/p-fake","manifest":{"plugin_id":"silo.seerr","capabilities":[
   {"type":"request_router.v1","display_name":"Fake Seerr","description":"Impostor."}]}}
]}`

func TestAuthorManifestInPluginsD(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) string {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	schema, err := filepath.Abs("../../assets/plugins/schema.json")
	if err != nil {
		t.Fatal(err)
	}

	main := write("plugins.yaml", `categories:
  - key: integration
    display: Integration
  - key: misc
    display: Misc
manifests:
  - id: official
    tier: official
    url: https://example.com/official.json
capability_categories:
  request_router.v1: integration
  auth_provider.v1: integration
plugins: []
`)
	write("upstream/official.json", officialFixture)
	write("upstream/someone.json", authorFixture)
	author := write("plugins.d/someone.yaml", `owners: [someone]
manifests:
  - id: someone
    tier: other
    category: misc
    url: https://example.com/someone.json
`)
	tmpl := write("page.tmpl", "{{ .Body }}\n")

	opts := options{
		pluginsFile:    main,
		pluginsDir:     filepath.Join(dir, "plugins.d"),
		schemaFile:     schema,
		templateFile:   tmpl,
		outputFile:     filepath.Join(dir, "PLUGINS.md"),
		manifestsDir:   filepath.Join(dir, "manifests"),
		updateManifest: true,
		manifestFrom:   filepath.Join(dir, "upstream"),
	}
	if err := run(opts); err != nil {
		t.Fatalf("run(-update-manifests) error = %v", err)
	}
	opts.updateManifest, opts.manifestFrom = false, ""
	if err := run(opts); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	got, _ := os.ReadFile(opts.outputFile)
	if !strings.Contains(string(got), "[Art](https://github.com/someone/p-art) | Other | ✅ | Artwork. |") {
		t.Fatalf("author manifest plugin missing or not placed by the fallback category:\n%s", got)
	}
	if strings.Contains(string(got), "Impostor") || strings.Contains(string(got), "p-fake") {
		t.Fatalf("an author manifest must not override an earlier plugin ID:\n%s", got)
	}

	// A broken author manifest keeps the previous snapshot instead of failing the refresh.
	opts.updateManifest, opts.manifestFrom = true, filepath.Join(dir, "missing")
	if err := os.MkdirAll(opts.manifestFrom, 0o755); err != nil {
		t.Fatal(err)
	}
	write("missing/official.json", officialFixture)
	if err := run(opts); err != nil {
		t.Fatalf("run(-update-manifests) with a broken author manifest error = %v, want it tolerated", err)
	}
	if _, err := os.Stat(filepath.Join(opts.manifestsDir, "someone.json")); err != nil {
		t.Fatalf("previous author snapshot was not kept: %v", err)
	}
	opts.updateManifest, opts.manifestFrom = false, ""

	// Authors cannot claim a tier above "other".
	write("plugins.d/someone.yaml", strings.Replace(mustRead(t, author), "tier: other", "tier: community", 1))
	if err := run(opts); err == nil || !strings.Contains(err.Error(), "tier \"other\"") {
		t.Fatalf("run() error = %v, want a rejection of non-other tiers in plugins.d", err)
	}

	// Manifest IDs must be unique across files.
	write("plugins.d/someone.yaml", strings.Replace(mustRead(t, author), "id: someone", "id: official", 1))
	if err := run(opts); err == nil || !strings.Contains(err.Error(), "declared in both") {
		t.Fatalf("run() error = %v, want a duplicate manifest id error", err)
	}
}

func TestSnapshotRejectsNonHTTPSRepoURL(t *testing.T) {
	_, err := snapshotFromUpstream("x", []byte(`{"plugins":[{"repo_url":"javascript:alert(1)","manifest":{"plugin_id":"a.b"}}]}`))
	if err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("snapshotFromUpstream() error = %v, want a non-https rejection", err)
	}
}

func TestSnapshotFromStandaloneManifest(t *testing.T) {
	snapshot, err := snapshotFromUpstream(
		"https://raw.githubusercontent.com/example/plugin/main/manifest.json",
		[]byte(`{
			"pluginId":"example.metadata",
			"capabilities":[{"type":"metadata_provider.v1","displayName":"Example Metadata","description":"Example provider."}],
			"presentation":{"displayName":"Example Plugin","summary":"Provides example metadata."}
		}`),
	)
	if err != nil {
		t.Fatalf("snapshotFromUpstream() error = %v", err)
	}
	if len(snapshot.Plugins) != 1 {
		t.Fatalf("snapshot has %d plugins, want 1", len(snapshot.Plugins))
	}
	plugin := snapshot.Plugins[0]
	if plugin.ID != "example.metadata" || plugin.Name != "Example Plugin" || plugin.Summary != "Provides example metadata." {
		t.Fatalf("snapshot plugin = %#v, want standalone manifest presentation metadata", plugin)
	}
	if plugin.RepoURL != "https://github.com/example/plugin" {
		t.Fatalf("snapshot repo URL = %q, want repository URL inferred from manifest URL", plugin.RepoURL)
	}
	if len(plugin.Capabilities) != 1 || plugin.Capabilities[0] != "metadata_provider.v1" {
		t.Fatalf("snapshot capabilities = %v, want metadata_provider.v1", plugin.Capabilities)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
