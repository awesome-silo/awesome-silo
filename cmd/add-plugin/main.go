package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

const (
	defaultPluginsFile = "assets/plugins/plugins.yaml"
	issueFieldHeading  = "### Plugin repository URL"
	githubAPIBase      = "https://api.github.com"
	maxResponseBytes   = 5 << 20
)

var githubPathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

type repositoryReference struct {
	Owner string
	Name  string
	URL   string
}

type repositoryInfo struct {
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Description   string `json:"description"`
	License       *struct {
		SPDXID string `json:"spdx_id"`
	} `json:"license"`
}

type repositoryFile struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type pluginManifest struct {
	PluginID     string `json:"plugin_id"`
	Category     string `json:"category"`
	Capabilities []struct {
		DisplayName string `json:"display_name"`
		Description string `json:"description"`
		Type        string `json:"type"`
	} `json:"capabilities"`
	Presentation struct {
		DisplayName string `json:"display_name"`
		Summary     string `json:"summary"`
	} `json:"presentation"`
}

type catalogConfig struct {
	Categories []struct {
		Key     string `yaml:"key"`
		Display string `yaml:"display"`
	} `yaml:"categories"`
	CapabilityCategories map[string]string `yaml:"capability_categories"`
	Plugins              []catalogPlugin   `yaml:"plugins"`
}

type catalogPlugin struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type pluginEntry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Category    string `yaml:"category"`
	Tier        string `yaml:"tier"`
	URL         string `yaml:"url"`
	OSS         string `yaml:"oss,omitempty"`
}

func main() {
	pluginsFile := flag.String("plugins", defaultPluginsFile, "path to plugins.yaml")
	flag.Parse()

	body := os.Getenv("ISSUE_BODY")
	repositoryURL, err := repositoryURLFromIssueBody(body)
	if err != nil {
		fail(err)
	}

	data, err := os.ReadFile(*pluginsFile)
	if err != nil {
		fail(fmt.Errorf("read %s: %w", *pluginsFile, err))
	}
	var catalog catalogConfig
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		fail(fmt.Errorf("parse %s: %w", *pluginsFile, err))
	}

	client := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	reference, err := parseRepositoryURL(repositoryURL)
	if err != nil {
		fail(err)
	}
	repository, manifest, err := fetchPlugin(client, githubAPIBase, reference)
	if err != nil {
		fail(err)
	}
	plugin, err := makePlugin(reference, repository, manifest, catalog)
	if err != nil {
		fail(err)
	}
	if err := appendPlugin(*pluginsFile, plugin); err != nil {
		fail(err)
	}

	fmt.Printf("Added %q from %s\n", plugin.Name, plugin.URL)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func repositoryURLFromIssueBody(body string) (string, error) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != issueFieldHeading {
			continue
		}
		for _, value := range lines[i+1:] {
			value = strings.TrimSpace(value)
			if value != "" {
				return value, nil
			}
		}
		return "", errors.New("the plugin repository URL field is empty")
	}
	return "", fmt.Errorf("issue body is missing the %q field", issueFieldHeading)
}

func parseRepositoryURL(raw string) (repositoryReference, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return repositoryReference{}, fmt.Errorf("invalid repository URL: %w", err)
	}
	if parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "github.com" ||
		parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return repositoryReference{}, errors.New("enter an https://github.com/owner/repository URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 {
		return repositoryReference{}, errors.New("the URL must point to a GitHub repository, not a file or subdirectory")
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if !githubPathSegment.MatchString(owner) || !githubPathSegment.MatchString(name) {
		return repositoryReference{}, errors.New("the URL contains an invalid GitHub owner or repository name")
	}
	return repositoryReference{
		Owner: owner,
		Name:  name,
		URL:   "https://github.com/" + owner + "/" + name,
	}, nil
}

func fetchPlugin(client *http.Client, apiBase string, reference repositoryReference) (repositoryInfo, pluginManifest, error) {
	var repository repositoryInfo
	repositoryPath := "/repos/" + path.Join(url.PathEscape(reference.Owner), url.PathEscape(reference.Name))
	if err := getJSON(client, apiBase+repositoryPath, &repository); err != nil {
		return repositoryInfo{}, pluginManifest{}, fmt.Errorf("read GitHub repository: %w", err)
	}
	if repository.Private {
		return repositoryInfo{}, pluginManifest{}, errors.New("the plugin repository must be public")
	}
	if repository.DefaultBranch == "" {
		return repositoryInfo{}, pluginManifest{}, errors.New("GitHub did not report a default branch for the repository")
	}

	query := url.Values{"ref": []string{repository.DefaultBranch}}
	manifestPath := repositoryPath + "/contents/manifest.json?" + query.Encode()
	var file repositoryFile
	if err := getJSON(client, apiBase+manifestPath, &file); err != nil {
		return repositoryInfo{}, pluginManifest{}, fmt.Errorf("read the repository's root manifest.json: %w", err)
	}
	if file.Type != "file" || file.Encoding != "base64" {
		return repositoryInfo{}, pluginManifest{}, errors.New("the repository's root manifest.json is not a regular GitHub file")
	}
	manifestData, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(file.Content), ""))
	if err != nil {
		return repositoryInfo{}, pluginManifest{}, fmt.Errorf("decode manifest.json: %w", err)
	}
	var manifest pluginManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return repositoryInfo{}, pluginManifest{}, fmt.Errorf("parse manifest.json: %w", err)
	}
	if strings.TrimSpace(manifest.PluginID) == "" {
		return repositoryInfo{}, pluginManifest{}, errors.New("manifest.json is missing plugin_id; only single-plugin manifests are supported")
	}
	return repository, manifest, nil
}

func getJSON(client *http.Client, requestURL string, destination any) error {
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "awesome-silo-plugin-submission")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request GitHub API: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read GitHub API response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("GitHub API response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(data))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		return fmt.Errorf("GitHub API returned %s: %s", response.Status, detail)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("parse GitHub API response: %w", err)
	}
	return nil
}

func makePlugin(reference repositoryReference, repository repositoryInfo, manifest pluginManifest, catalog catalogConfig) (pluginEntry, error) {
	name := strings.TrimSpace(manifest.Presentation.DisplayName)
	description := strings.TrimSpace(manifest.Presentation.Summary)
	if name == "" && len(manifest.Capabilities) > 0 {
		name = strings.TrimSpace(manifest.Capabilities[0].DisplayName)
	}
	if description == "" && len(manifest.Capabilities) > 0 {
		description = strings.TrimSpace(manifest.Capabilities[0].Description)
	}
	if description == "" {
		description = strings.TrimSpace(repository.Description)
	}
	name = strings.Join(strings.Fields(name), " ")
	description = strings.Join(strings.Fields(description), " ")
	if name == "" || description == "" {
		return pluginEntry{}, errors.New("manifest.json must provide a plugin name and description (or GitHub repository description)")
	}
	if strings.ContainsAny(name, "[]|") {
		return pluginEntry{}, errors.New("plugin name contains Markdown link characters; use plain text in manifest.json")
	}
	if strings.ContainsAny(description, "[]<>`") {
		return pluginEntry{}, errors.New("plugin description must be plain text in manifest.json")
	}

	category := resolveCategory(manifest.Category, manifest.Capabilities, catalog)
	if category == "" {
		return pluginEntry{}, fmt.Errorf("could not determine a catalog category from manifest category %q or its capabilities", manifest.Category)
	}
	plugin := pluginEntry{
		Name:        name,
		Description: description,
		Category:    category,
		Tier:        "other",
		URL:         reference.URL,
	}
	if repository.License != nil && repository.License.SPDXID != "" && repository.License.SPDXID != "NOASSERTION" {
		plugin.OSS = reference.URL
	}
	return plugin, nil
}

func resolveCategory(manifestCategory string, capabilities []struct {
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}, catalog catalogConfig) string {
	for _, category := range catalog.Categories {
		if sameCategoryName(manifestCategory, category.Key) || sameCategoryName(manifestCategory, category.Display) {
			return category.Key
		}
	}
	for _, capability := range capabilities {
		if category, ok := catalog.CapabilityCategories[capability.Type]; ok {
			for _, known := range catalog.Categories {
				if category == known.Key {
					return category
				}
			}
		}
	}
	return ""
}

func sameCategoryName(left, right string) bool {
	normalize := func(value string) string {
		var result strings.Builder
		for _, r := range strings.ToLower(value) {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				result.WriteRune(r)
			}
		}
		return result.String()
	}
	left, right = normalize(left), normalize(right)
	if left == right {
		return true
	}
	aliases := map[string]string{
		"uicustomization":        "ui",
		"collectionsplaylists":   "collections",
		"integrationandsync":     "integration",
		"authentication":         "auth",
		"librarymanagement":      "library",
		"metadataproviders":      "metadata",
		"networkingremoteaccess": "network",
	}
	if category, ok := aliases[left]; ok {
		return category == right
	}
	if category, ok := aliases[right]; ok {
		return category == left
	}
	return false
}

func appendPlugin(filename string, plugin pluginEntry) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read %s: %w", filename, err)
	}
	var catalog catalogConfig
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return fmt.Errorf("parse %s: %w", filename, err)
	}
	for _, existing := range catalog.Plugins {
		if strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(plugin.Name)) {
			return fmt.Errorf("plugin %q is already listed in %s", plugin.Name, filename)
		}
		if strings.EqualFold(strings.TrimRight(existing.URL, "/"), strings.TrimRight(plugin.URL, "/")) {
			return fmt.Errorf("repository %s is already listed in %s", plugin.URL, filename)
		}
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse YAML nodes in %s: %w", filename, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s must contain a YAML mapping", filename)
	}
	pluginsNode := mappingValue(document.Content[0], "plugins")
	if pluginsNode == nil || pluginsNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s must contain a plugins sequence", filename)
	}
	pluginsNode.Content = append(pluginsNode.Content, pluginYAMLNode(plugin))

	var output strings.Builder
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("encode %s: %w", filename, err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("finish encoding %s: %w", filename, err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("stat %s: %w", filename, err)
	}
	if err := os.WriteFile(filename, []byte(output.String()), info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", filename, err)
	}
	return nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func pluginYAMLNode(plugin pluginEntry) *yaml.Node {
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(key, value string, style yaml.Style) {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style},
		)
	}
	add("name", plugin.Name, yaml.DoubleQuotedStyle)
	add("description", plugin.Description, yaml.DoubleQuotedStyle)
	add("category", plugin.Category, 0)
	add("tier", plugin.Tier, 0)
	add("url", plugin.URL, 0)
	if plugin.OSS != "" {
		add("oss", plugin.OSS, 0)
	}
	return mapping
}
