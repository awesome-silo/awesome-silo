package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseRepositoryURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantURL string
		wantErr string
	}{
		{name: "repository", input: "https://github.com/neilmc78/silo-plugin-boxset-collections/", wantURL: "https://github.com/neilmc78/silo-plugin-boxset-collections"},
		{name: "git suffix", input: "https://github.com/neilmc78/plugin.git", wantURL: "https://github.com/neilmc78/plugin"},
		{name: "non-GitHub host", input: "https://github.com.evil.example/owner/plugin", wantErr: "https://github.com"},
		{name: "non-HTTPS", input: "http://github.com/owner/plugin", wantErr: "https://github.com"},
		{name: "subdirectory", input: "https://github.com/owner/plugin/tree/main", wantErr: "not a file or subdirectory"},
		{name: "query", input: "https://github.com/owner/plugin?tab=readme", wantErr: "https://github.com"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRepositoryURL(test.input)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("parseRepositoryURL() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRepositoryURL() error = %v", err)
			}
			if got.URL != test.wantURL {
				t.Fatalf("parseRepositoryURL() URL = %q, want %q", got.URL, test.wantURL)
			}
		})
	}
}

func TestRepositoryURLFromIssueBody(t *testing.T) {
	body := "### Some other field\n\nValue\n\n### Plugin repository URL\n\nhttps://github.com/owner/plugin\n"
	got, err := repositoryURLFromIssueBody(body)
	if err != nil {
		t.Fatalf("repositoryURLFromIssueBody() error = %v", err)
	}
	if got != "https://github.com/owner/plugin" {
		t.Fatalf("repositoryURLFromIssueBody() = %q", got)
	}
	if _, err := repositoryURLFromIssueBody("### Other\n\nvalue\n"); err == nil {
		t.Fatal("repositoryURLFromIssueBody() should reject a missing URL field")
	}
}

func TestFetchPluginUsesPublicGitHubManifest(t *testing.T) {
	manifest := `{"plugin_id":"example.collections.boxsets","category":"Collections","presentation":{"display_name":"Box Set Collections","summary":"Keeps box sets in step with your library."},"capabilities":[{"type":"event_consumer.v1"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/plugin":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"private":        false,
				"default_branch": "main",
				"license":        map[string]string{"spdx_id": "AGPL-3.0-or-later"},
			})
		case r.URL.Path == "/repos/owner/plugin/contents/manifest.json" && r.URL.Query().Get("ref") == "main":
			_ = json.NewEncoder(w).Encode(repositoryFile{
				Type:     "file",
				Encoding: "base64",
				Content:  base64.StdEncoding.EncodeToString([]byte(manifest)),
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reference, _ := parseRepositoryURL("https://github.com/owner/plugin")
	repository, got, err := fetchPlugin(server.Client(), server.URL, reference)
	if err != nil {
		t.Fatalf("fetchPlugin() error = %v", err)
	}
	if repository.License == nil || repository.License.SPDXID != "AGPL-3.0-or-later" {
		t.Fatalf("fetchPlugin() license = %#v", repository.License)
	}
	if got.PluginID != "example.collections.boxsets" || got.Presentation.DisplayName != "Box Set Collections" {
		t.Fatalf("fetchPlugin() manifest = %#v", got)
	}
}

func TestMakePluginUsesManifestCategoryBeforeCapabilities(t *testing.T) {
	var catalog catalogConfig
	if err := yaml.Unmarshal([]byte(`
categories:
  - key: collections
    display: "📂 Collections & Playlists"
  - key: playback
    display: "▶️ Playback"
capability_categories:
  event_consumer.v1: playback
`), &catalog); err != nil {
		t.Fatal(err)
	}
	reference, _ := parseRepositoryURL("https://github.com/owner/plugin")
	repository := repositoryInfo{License: &struct {
		SPDXID string `json:"spdx_id"`
	}{SPDXID: "AGPL-3.0-or-later"}}
	manifest := pluginManifest{
		PluginID: "example.collections.boxsets",
		Category: "Collections",
		Presentation: struct {
			DisplayName string `json:"display_name"`
			Summary     string `json:"summary"`
		}{DisplayName: "Box Set Collections", Summary: "Keeps box sets in step with your library."},
		Capabilities: []struct {
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
			Type        string `json:"type"`
		}{{Type: "event_consumer.v1"}},
	}
	got, err := makePlugin(reference, repository, manifest, catalog)
	if err != nil {
		t.Fatalf("makePlugin() error = %v", err)
	}
	if got.Category != "collections" {
		t.Fatalf("makePlugin() category = %q, want collections", got.Category)
	}
	if got.Tier != "other" || got.OSS != reference.URL {
		t.Fatalf("makePlugin() tier/OSS = %q/%q", got.Tier, got.OSS)
	}
}

func TestAppendPluginPreservesExistingYAMLAndRejectsDuplicate(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "plugins.yaml")
	original := `# preserve this comment
categories:
  - key: collections
    display: "📂 Collections & Playlists"

plugins:
  - name: "Existing"
    description: "An existing entry."
    category: collections
    tier: other
    url: https://github.com/owner/existing
`
	if err := os.WriteFile(filename, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	plugin := pluginEntry{
		Name:        "Box Set Collections",
		Description: "Keeps box sets in step with your library.",
		Category:    "collections",
		Tier:        "other",
		URL:         "https://github.com/owner/plugin",
		OSS:         "https://github.com/owner/plugin",
	}
	if err := appendPlugin(filename, plugin); err != nil {
		t.Fatalf("appendPlugin() error = %v", err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# preserve this comment") {
		t.Fatal("appendPlugin() did not preserve the existing comment")
	}
	var catalog catalogConfig
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("updated YAML is invalid: %v", err)
	}
	if len(catalog.Plugins) != 2 || catalog.Plugins[1].Name != plugin.Name {
		t.Fatalf("updated plugins = %#v", catalog.Plugins)
	}
	if err := appendPlugin(filename, plugin); err == nil || !strings.Contains(err.Error(), "already listed") {
		t.Fatalf("appendPlugin() duplicate error = %v", err)
	}
}
