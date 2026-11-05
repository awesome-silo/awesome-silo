package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxManifestBytes = 20 << 20

// Manifest configures one remote plugin manifest in plugins.yaml.
type Manifest struct {
	ID                 string   `yaml:"id"`
	Tier               string   `yaml:"tier"`
	Category           string   `yaml:"category"`
	URL                string   `yaml:"url"`
	UnsupportedPlugins []string `yaml:"unsupported_plugins"`
	Source             string   `yaml:"-"` // file the manifest is declared in
}

func (m Manifest) snapshotPath(dir string) string {
	return filepath.Join(dir, m.ID+".json")
}

// manifestPlugin is the subset of a manifest entry kept in the checked-in snapshot.
type manifestPlugin struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Summary      string   `json:"summary"`
	RepoURL      string   `json:"repo_url"`
	Capabilities []string `json:"capabilities"`
}

type manifestSnapshot struct {
	Source  string           `json:"source"`
	Plugins []manifestPlugin `json:"plugins"`
}

// upstreamManifest mirrors only the fields we read from the upstream manifest.
type upstreamManifest struct {
	PluginID     string                `json:"plugin_id"`
	PluginIDAlt  string                `json:"pluginId"`
	Capabilities []upstreamCapability  `json:"capabilities"`
	Presentation *upstreamPresentation `json:"presentation"`
	Plugins      []struct {
		RepoURL  string `json:"repo_url"`
		Manifest struct {
			PluginID     string                `json:"plugin_id"`
			PluginIDAlt  string                `json:"pluginId"`
			Capabilities []upstreamCapability  `json:"capabilities"`
			Presentation *upstreamPresentation `json:"presentation"`
		} `json:"manifest"`
	} `json:"plugins"`
}

type upstreamCapability struct {
	Type           string `json:"type"`
	DisplayName    string `json:"display_name"`
	DisplayNameAlt string `json:"displayName"`
	Description    string `json:"description"`
}

type upstreamPresentation struct {
	DisplayName    string `json:"display_name"`
	DisplayNameAlt string `json:"displayName"`
	Summary        string `json:"summary"`
	SourceURL      string `json:"source_url"`
	SourceURLAlt   string `json:"sourceUrl"`
	HomepageURL    string `json:"homepage_url"`
	HomepageURLAlt string `json:"homepageUrl"`
}

func snapshotFromUpstream(source string, data []byte) (*manifestSnapshot, error) {
	var upstream upstreamManifest
	if err := json.Unmarshal(data, &upstream); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	snapshot := &manifestSnapshot{Source: source}
	addPlugin := func(pluginID, repoURL string, capabilities []upstreamCapability, presentation *upstreamPresentation) error {
		if pluginID == "" {
			return fmt.Errorf("manifest entry for %q has no plugin_id", repoURL)
		}
		if repoURL == "" && presentation != nil {
			repoURL = firstNonEmpty(presentation.SourceURL, presentation.SourceURLAlt)
			if repoURL == "" {
				repoURL = firstNonEmpty(presentation.HomepageURL, presentation.HomepageURLAlt)
			}
		}
		if repoURL == "" {
			return fmt.Errorf("manifest plugin %q has no repo_url or presentation source/homepage URL", pluginID)
		}
		if !strings.HasPrefix(repoURL, "https://") {
			return fmt.Errorf("manifest plugin %q has a repo_url that is not https: %q", pluginID, repoURL)
		}

		plugin := manifestPlugin{ID: pluginID, RepoURL: repoURL}
		for _, capability := range capabilities {
			plugin.Capabilities = append(plugin.Capabilities, capability.Type)
		}
		if presentation != nil {
			plugin.Name = firstNonEmpty(presentation.DisplayName, presentation.DisplayNameAlt)
			plugin.Summary = presentation.Summary
		}
		if len(capabilities) > 0 {
			if plugin.Name == "" {
				plugin.Name = firstNonEmpty(capabilities[0].DisplayName, capabilities[0].DisplayNameAlt)
			}
			if plugin.Summary == "" {
				plugin.Summary = capabilities[0].Description
			}
		}
		if plugin.Name == "" {
			plugin.Name = pluginID
		}
		snapshot.Plugins = append(snapshot.Plugins, plugin)
		return nil
	}

	pluginID := firstNonEmpty(upstream.PluginID, upstream.PluginIDAlt)
	if pluginID != "" {
		if err := addPlugin(pluginID, repositoryURLFromManifest(source), upstream.Capabilities, upstream.Presentation); err != nil {
			return nil, err
		}
	}
	for _, entry := range upstream.Plugins {
		m := entry.Manifest
		pluginID := firstNonEmpty(m.PluginID, m.PluginIDAlt)
		if err := addPlugin(pluginID, entry.RepoURL, m.Capabilities, m.Presentation); err != nil {
			return nil, err
		}
	}
	sort.Slice(snapshot.Plugins, func(i, j int) bool { return snapshot.Plugins[i].ID < snapshot.Plugins[j].ID })
	return snapshot, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func repositoryURLFromManifest(source string) string {
	const prefix = "https://raw.githubusercontent.com/"
	if !strings.HasPrefix(source, prefix) {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(source, prefix), "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return "https://github.com/" + parts[0] + "/" + parts[1]
}

func fetchManifest(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", url, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", url, maxManifestBytes)
	}
	return data, nil
}

// updateManifestSnapshot writes the snapshot for manifest into dir, reading the
// upstream JSON from fromDir/<id>.json when fromDir is set and from its URL otherwise.
func updateManifestSnapshot(manifest Manifest, dir, fromDir string) error {
	var data []byte
	var err error
	if fromDir != "" {
		data, err = os.ReadFile(filepath.Join(fromDir, manifest.ID+".json"))
	} else {
		data, err = fetchManifest(manifest.URL)
	}
	if err != nil {
		return err
	}

	snapshot, err := snapshotFromUpstream(manifest.URL, data)
	if err != nil {
		return fmt.Errorf("%s manifest: %w", manifest.ID, err)
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := manifest.snapshotPath(dir)
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("Wrote %d %s plugins to %s\n", len(snapshot.Plugins), manifest.Tier, path)
	return nil
}

func loadManifestSnapshot(path string) (*manifestSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read official manifest snapshot %s (create it with `go run ./cmd/generate-plugins -update-manifests`): %w", path, err)
	}
	var snapshot manifestSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &snapshot, nil
}

// manifestPlugins converts a snapshot to plugin entries of the manifest's tier,
// placing each by the first of its capabilities that has a configured category,
// or else by the manifest's fallback category.
func manifestPlugins(snapshot *manifestSnapshot, manifest Manifest, categories map[string]string) ([]Plugin, error) {
	plugins := make([]Plugin, 0, len(snapshot.Plugins))
	unsupported := make(map[string]struct{}, len(manifest.UnsupportedPlugins))
	for _, id := range manifest.UnsupportedPlugins {
		unsupported[id] = struct{}{}
	}
	for _, entry := range snapshot.Plugins {
		category := ""
		for _, capability := range entry.Capabilities {
			if mapped, ok := categories[capability]; ok {
				category = mapped
				break
			}
		}
		if category == "" {
			category = manifest.Category
		}
		if category == "" {
			return nil, fmt.Errorf("%s plugin %q has no capability mapped in capability_categories and its manifest %q has no fallback category (capabilities: %v)", manifest.Tier, entry.ID, manifest.ID, entry.Capabilities)
		}
		_, isUnsupported := unsupported[entry.ID]
		delete(unsupported, entry.ID)
		plugins = append(plugins, Plugin{
			ID:          entry.ID,
			Name:        entry.Name,
			Description: entry.Summary,
			Category:    category,
			Tier:        manifest.Tier,
			URL:         entry.RepoURL,
			OSS:         entry.RepoURL,
			Unsupported: isUnsupported,
		})
	}
	if len(unsupported) > 0 {
		for _, id := range manifest.UnsupportedPlugins {
			if _, ok := unsupported[id]; ok {
				return nil, fmt.Errorf("%s manifest %q marks unknown plugin ID %q unsupported", manifest.Tier, manifest.ID, id)
			}
		}
	}
	return plugins, nil
}

// validateManifests checks that manifest IDs are unique and that manifests
// declared in plugins.d files use only the "other" tier.
func validateManifests(manifests []Manifest, extras []*file, paths []string) error {
	ids := make(map[string]string, len(manifests))
	for _, manifest := range manifests {
		if previous, dup := ids[manifest.ID]; dup {
			return fmt.Errorf("manifest id %q is declared in both %s and %s", manifest.ID, previous, manifest.Source)
		}
		ids[manifest.ID] = manifest.Source
	}
	for i, extra := range extras {
		for _, manifest := range extra.Manifests {
			if manifest.Tier != "other" {
				return fmt.Errorf("%s: manifest %q has tier %q; manifests in plugins.d can only use tier \"other\"", paths[i], manifest.ID, manifest.Tier)
			}
		}
	}
	return nil
}

// updateManifestSnapshots refreshes every snapshot and removes snapshots that no
// manifest refers to. A failure to refresh an "other" manifest keeps its previous
// snapshot, so one author's broken URL cannot block the daily refresh.
func updateManifestSnapshots(manifests []Manifest, dir, fromDir string) error {
	keep := make(map[string]struct{}, len(manifests))
	for _, manifest := range manifests {
		keep[manifest.ID+".json"] = struct{}{}
		if err := updateManifestSnapshot(manifest, dir, fromDir); err != nil {
			if manifest.Tier != "other" {
				return err
			}
			fmt.Fprintf(os.Stderr, "warning: keeping the previous snapshot of %s (%s): %v\n", manifest.ID, manifest.Source, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, ok := keep[entry.Name()]; !ok && filepath.Ext(entry.Name()) == ".json" {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
			fmt.Printf("Removed unused snapshot %s\n", entry.Name())
		}
	}
	return nil
}
