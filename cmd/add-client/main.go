package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type client struct {
	Name      string     `yaml:"name"`
	Targets   []string   `yaml:"targets"`
	Types     []string   `yaml:"types"`
	Website   string     `yaml:"website"`
	OSS       string     `yaml:"oss,omitempty"`
	Official  bool       `yaml:"official"`
	Price     price      `yaml:"price"`
	Downloads []download `yaml:"downloads"`
}

type price struct {
	Free bool `yaml:"free"`
	Paid bool `yaml:"paid"`
}

type download struct {
	Type string `yaml:"type"`
	Text string `yaml:"text"`
	URL  string `yaml:"url"`
}

type catalog struct {
	Clients []client `yaml:"clients"`
	Targets []struct {
		Has []struct {
			Name string `yaml:"name"`
		} `yaml:"has"`
	} `yaml:"targets"`
}

func main() {
	clientsFile := flag.String("clients", "assets/clients/clients.yaml", "path to clients.yaml")
	clientsDir := flag.String("clients-dir", "assets/clients/clients.d", "directory of maintainer-owned client YAML files")
	flag.Parse()
	if err := run(os.Getenv("ISSUE_BODY"), *clientsFile, *clientsDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(body, filename, directory string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("read clients file: %w", err)
	}
	var config catalog
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse clients file: %w", err)
	}
	entry, err := parseSubmission(body, config)
	if err != nil {
		return err
	}
	if err := checkDuplicate(entry, config.Clients); err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.yaml"))
	if err != nil {
		return fmt.Errorf("find maintainer-owned files: %w", err)
	}
	for _, path := range paths {
		extraData, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var extra catalog
		if err := yaml.Unmarshal(extraData, &extra); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if err := checkDuplicate(entry, extra.Clients); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	updated, err := insertClient(data, entry)
	if err != nil {
		return err
	}
	info, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("stat clients file: %w", err)
	}
	if err := os.WriteFile(filename, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write clients file: %w", err)
	}
	fmt.Printf("Added client %q\n", entry.Name)
	return nil
}

func issueFields(body string) (map[string]string, error) {
	fields := make(map[string]string)
	var heading string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "### ") {
			heading = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if _, exists := fields[heading]; exists {
				return nil, fmt.Errorf("duplicate issue field %q", heading)
			}
			fields[heading] = ""
		} else if heading != "" {
			fields[heading] += line + "\n"
		}
	}
	for name, value := range fields {
		fields[name] = strings.TrimSpace(value)
	}
	return fields, nil
}

func parseSubmission(body string, config catalog) (client, error) {
	fields, err := issueFields(body)
	if err != nil {
		return client{}, err
	}
	name := fields["Client name"]
	if err := plainText(name); err != nil {
		return client{}, fmt.Errorf("client name: %w", err)
	}
	website := fields["Client website"]
	if err := webURL(website); err != nil {
		return client{}, fmt.Errorf("client website: %w", err)
	}
	source := fields["Open-source repository URL"]
	if source == "_No response_" {
		source = ""
	}
	if source != "" {
		if err := webURL(source); err != nil {
			return client{}, fmt.Errorf("open-source repository URL: %w", err)
		}
	}
	compatibility := fields["Compatibility"]
	if compatibility != "Native" && compatibility != "Compatible" {
		return client{}, errors.New("compatibility must be Native or Compatible")
	}

	knownTargets := make(map[string]bool)
	for _, group := range config.Targets {
		for _, target := range group.Has {
			knownTargets[target.Name] = true
		}
	}
	var targets []string
	seenTargets := make(map[string]bool)
	for _, line := range strings.Split(fields["Targets"], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- [ ] ") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(line), "- [x] ") {
			return client{}, errors.New("targets must be selected from the issue form")
		}
		target := strings.TrimSpace(line[len("- [x] "):])
		if !knownTargets[target] || seenTargets[target] {
			return client{}, fmt.Errorf("unknown or duplicate target %q", target)
		}
		seenTargets[target] = true
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return client{}, errors.New("select at least one target")
	}

	var cost price
	switch fields["Price"] {
	case "Free":
		cost.Free = true
	case "Paid":
		cost.Paid = true
	case "Free and paid":
		cost.Free, cost.Paid = true, true
	default:
		return client{}, errors.New("price must be Free, Paid, or Free and paid")
	}

	var downloads []download
	for _, line := range strings.Split(fields["Download links"], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			return client{}, errors.New("download links must be one Name | URL per line")
		}
		label, link := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if err := plainText(label); err != nil {
			return client{}, fmt.Errorf("download name: %w", err)
		}
		if err := webURL(link); err != nil {
			return client{}, fmt.Errorf("download URL: %w", err)
		}
		downloads = append(downloads, download{Type: "text", Text: label, URL: link})
	}
	if len(downloads) == 0 {
		return client{}, errors.New("provide at least one download link")
	}

	return client{Name: name, Website: website, OSS: source, Targets: targets, Types: []string{compatibility}, Price: cost, Downloads: downloads}, nil
}

func plainText(value string) error {
	if value == "" || value == "_No response_" || strings.ContainsAny(value, "\r\n[]<>|`\\") {
		return errors.New("must be nonempty plain text on one line")
	}
	return nil
}

func webURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(value, " \t\r\n<>[]()|\\") {
		return errors.New("must be a valid http or https URL without credentials or whitespace")
	}
	return nil
}

func checkDuplicate(entry client, existing []client) error {
	normalizeURL := func(value string) string {
		return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "/"))
	}
	for _, item := range existing {
		if strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(entry.Name)) {
			return fmt.Errorf("client name %q is already listed", entry.Name)
		}
		for _, candidate := range []string{entry.Website, entry.OSS} {
			for _, known := range []string{item.Website, item.OSS} {
				if candidate != "" && known != "" && normalizeURL(candidate) == normalizeURL(known) {
					return fmt.Errorf("client URL %q is already listed for %q", candidate, item.Name)
				}
			}
		}
	}
	return nil
}

func insertClient(data []byte, entry client) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse client YAML: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("clients file must be a YAML mapping")
	}
	root := doc.Content[0]
	var clientsNode *yaml.Node
	nextLine := 0
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "clients" {
			clientsNode = root.Content[i+1]
			if i+2 < len(root.Content) {
				nextLine = root.Content[i+2].Line
			}
			break
		}
	}
	if clientsNode == nil || clientsNode.Kind != yaml.SequenceNode || nextLine == 0 {
		return nil, errors.New("clients file must have a clients sequence followed by another top-level section")
	}
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(struct {
		Clients []client `yaml:"clients"`
	}{Clients: []client{entry}}); err != nil {
		return nil, fmt.Errorf("encode client: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("finish encoding client: %w", err)
	}
	const prefix = "clients:\n"
	if !strings.HasPrefix(encoded.String(), prefix) {
		return nil, errors.New("encoded client is missing its sequence")
	}
	lines := strings.SplitAfter(string(data), "\n")
	if nextLine < 1 || nextLine > len(lines) {
		return nil, errors.New("invalid next section position in client YAML")
	}
	before := strings.Join(lines[:nextLine-1], "")
	after := strings.Join(lines[nextLine-1:], "")
	result := before + strings.TrimPrefix(encoded.String(), prefix) + "\n" + after
	return []byte(result), nil
}
