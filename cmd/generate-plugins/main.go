package main

import (
	"bytes"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/awesome-silo/awesome-silo/internal/yamlschema"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

type Category struct {
	Key     string `yaml:"key"`
	Display string `yaml:"display"`
}

type Plugin struct {
	ID          string `yaml:"-"` // set for manifest plugins only
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	Tier        string `yaml:"tier"`
	URL         string `yaml:"url"`
	OSS         string `yaml:"oss"`
	Beta        bool   `yaml:"beta"`
	Unsupported bool   `yaml:"unsupported"`
}

type file struct {
	Owners               []string          `yaml:"owners"`
	Categories           []Category        `yaml:"categories"`
	Manifests            []Manifest        `yaml:"manifests"`
	CapabilityCategories map[string]string `yaml:"capability_categories"`
	Plugins              []Plugin          `yaml:"plugins"`
}

type options struct {
	pluginsFile    string
	pluginsDir     string
	schemaFile     string
	templateFile   string
	outputFile     string
	manifestsDir   string
	check          bool
	updateManifest bool
	manifestFrom   string
}

type tier struct {
	key     string
	display string
}

// Tiers are listed in display priority order.
var tiers = []tier{
	{"official", "🔹 Official"},
	{"community", "👥 Community"},
	{"other", "Other"},
}

type pageData struct {
	Body string
}

func main() {
	var opts options
	flag.StringVar(&opts.pluginsFile, "plugins", "assets/plugins/plugins.yaml", "path to plugins.yaml")
	flag.StringVar(&opts.pluginsDir, "plugins-dir", "assets/plugins/plugins.d", "directory of author-managed plugin YAML files")
	flag.StringVar(&opts.schemaFile, "schema", "assets/plugins/schema.json", "path to the plugin JSON schema")
	flag.StringVar(&opts.templateFile, "template", "assets/plugins/PLUGINS.md.tmpl", "path to the Markdown page template")
	flag.StringVar(&opts.outputFile, "out", "PLUGINS.md", "output path")
	flag.StringVar(&opts.manifestsDir, "manifests-dir", "assets/plugins/manifests", "directory of checked-in manifest snapshots")
	flag.BoolVar(&opts.check, "check", false, "fail if the output file is not up to date")
	flag.BoolVar(&opts.updateManifest, "update-manifests", false, "refresh the manifest snapshots from their URLs instead of generating")
	flag.StringVar(&opts.manifestFrom, "manifests-from", "", "with -update-manifests, read upstream manifests from <dir>/<id>.json instead of their URLs")
	flag.Parse()

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(opts options) error {
	schema, err := jsonschema.Compile(opts.schemaFile)
	if err != nil {
		return fmt.Errorf("compile plugin schema: %w", err)
	}

	main, err := load(schema, opts.pluginsFile)
	if err != nil {
		return err
	}
	for i := range main.Manifests {
		main.Manifests[i].Source = opts.pluginsFile
	}

	paths, err := filepath.Glob(filepath.Join(opts.pluginsDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("find plugin files: %w", err)
	}
	sort.Strings(paths)
	var extras []*file
	for _, path := range paths {
		extra, err := load(schema, path)
		if err != nil {
			return err
		}
		for i := range extra.Manifests {
			extra.Manifests[i].Source = path
		}
		extras = append(extras, extra)
	}

	// Manifests from plugins.yaml come first, then those from plugins.d files in file order.
	manifests := append([]Manifest(nil), main.Manifests...)
	for _, extra := range extras {
		manifests = append(manifests, extra.Manifests...)
	}
	if err := validateManifests(manifests, extras, paths); err != nil {
		return err
	}

	if opts.updateManifest {
		if len(manifests) == 0 {
			return fmt.Errorf("no manifests are configured")
		}
		return updateManifestSnapshots(manifests, opts.manifestsDir, opts.manifestFrom)
	}

	categories := main.Categories
	var plugins []Plugin
	// A plugin ID already provided by an earlier manifest is skipped in later ones.
	seenIDs := make(map[string]struct{})
	for _, manifest := range manifests {
		snapshot, err := loadManifestSnapshot(manifest.snapshotPath(opts.manifestsDir))
		if err != nil {
			return err
		}
		fromManifest, err := manifestPlugins(snapshot, manifest, main.CapabilityCategories)
		if err != nil {
			return err
		}
		for _, plugin := range fromManifest {
			if _, dup := seenIDs[plugin.ID]; dup {
				continue
			}
			seenIDs[plugin.ID] = struct{}{}
			plugins = append(plugins, plugin)
		}
	}
	plugins = append(plugins, main.Plugins...)
	for _, extra := range extras {
		plugins = append(plugins, extra.Plugins...)
	}
	plugins = filterBlacklistedOwners(plugins, strings.FieldsFunc(os.Getenv("PLUGIN_BLACKLISTED_OWNERS"), func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}))

	if err := validate(categories, plugins); err != nil {
		return err
	}

	tmpl, err := template.ParseFiles(opts.templateFile)
	if err != nil {
		return fmt.Errorf("parse %s: %w", opts.templateFile, err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, pageData{Body: render(categories, plugins)}); err != nil {
		return fmt.Errorf("render %s: %w", opts.templateFile, err)
	}

	if opts.check {
		current, err := os.ReadFile(opts.outputFile)
		if err != nil {
			return fmt.Errorf("read generated file %s: %w", opts.outputFile, err)
		}
		if !bytes.Equal(current, output.Bytes()) {
			return fmt.Errorf("%s is out of date; run `go run ./cmd/generate-plugins`", opts.outputFile)
		}
		fmt.Printf("%s is up to date\n", opts.outputFile)
		return nil
	}

	if err := os.WriteFile(opts.outputFile, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write generated file %s: %w", opts.outputFile, err)
	}
	fmt.Printf("Generated %s from %s and %d files in %s\n", opts.outputFile, opts.pluginsFile, len(paths), opts.pluginsDir)
	return nil
}

func load(schema *jsonschema.Schema, path string) (*file, error) {
	if err := yamlschema.Validate(schema, path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

func validate(categories []Category, plugins []Plugin) error {
	known := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		if _, dup := known[category.Key]; dup {
			return fmt.Errorf("duplicate category %q", category.Key)
		}
		known[category.Key] = struct{}{}
	}

	names := make(map[string]struct{}, len(plugins))
	for _, plugin := range plugins {
		if _, ok := known[plugin.Category]; !ok {
			return fmt.Errorf("plugin %q references undefined category %q", plugin.Name, plugin.Category)
		}
		key := strings.ToLower(strings.TrimSpace(plugin.Name))
		if _, dup := names[key]; dup {
			return fmt.Errorf("duplicate plugin %q", plugin.Name)
		}
		names[key] = struct{}{}
	}
	return nil
}

func filterBlacklistedOwners(plugins []Plugin, owners []string) []Plugin {
	if len(owners) == 0 {
		return plugins
	}

	blocked := make(map[string]struct{}, len(owners))
	for _, owner := range owners {
		owner = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(owner, "@")))
		if owner != "" {
			blocked[owner] = struct{}{}
		}
	}

	filtered := make([]Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		_, blockedURL := blocked[githubOwner(plugin.URL)]
		_, blockedOSS := blocked[githubOwner(plugin.OSS)]
		if !blockedURL && !blockedOSS {
			filtered = append(filtered, plugin)
		}
	}
	return filtered
}

func githubOwner(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return strings.ToLower(parts[0])
}

func render(categories []Category, plugins []Plugin) string {
	var sections []string
	var unsupported []Plugin
	for _, plugin := range plugins {
		if plugin.Unsupported {
			unsupported = append(unsupported, plugin)
		}
	}
	for _, category := range categories {
		var inCategory []Plugin
		for _, plugin := range plugins {
			if plugin.Category == category.Key && !plugin.Unsupported {
				inCategory = append(inCategory, plugin)
			}
		}
		if len(inCategory) == 0 {
			continue
		}
		sortPlugins(inCategory)
		sections = append(sections, "## "+category.Display+"\n\n"+table(inCategory))
	}
	if len(unsupported) > 0 {
		sortPlugins(unsupported)
		sections = append(sections, "## Unsupported Plugins\n\n"+
			"These plugins implement functionality outside Silo's supported scope. Silo provides no support for them; asking for help in any official Silo channel, including Discord, will result in a ban.\n\n"+
			table(unsupported))
	}
	if len(sections) == 0 {
		return "_No plugins are listed yet._"
	}
	return strings.Join(sections, "\n\n")
}

func sortPlugins(plugins []Plugin) {
	sort.SliceStable(plugins, func(i, j int) bool {
		if ri, rj := tierRank(plugins[i].Tier), tierRank(plugins[j].Tier); ri != rj {
			return ri < rj
		}
		return strings.ToLower(plugins[i].Name) < strings.ToLower(plugins[j].Name)
	})
}

func tierRank(key string) int {
	for i, t := range tiers {
		if t.key == key {
			return i
		}
	}
	return len(tiers)
}

func tierDisplay(key string) string {
	for _, t := range tiers {
		if t.key == key {
			return t.display
		}
	}
	return key
}

func table(plugins []Plugin) string {
	var b strings.Builder
	b.WriteString("| Name | Tier | OSS | Description |\n")
	b.WriteString("| ---- | ---- | --- | ----------- |\n")
	for _, plugin := range plugins {
		name := plugin.Name
		if plugin.Beta {
			name += " ` ⚒️ `"
		}
		oss := "❌"
		if plugin.OSS != "" {
			oss = "✅"
		}
		description := plugin.Description
		if plugin.Unsupported {
			description += " ⚠️ **UNSUPPORTED by Silo**"
		}
		fmt.Fprintf(&b, "| [%s](%s) | %s | %s | %s |\n",
			name, plugin.URL, tierDisplay(plugin.Tier), oss, escapeCell(description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func escapeCell(value string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(value), " "), "|", `\|`)
}
