package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const submission = `### Client name

Example Client

### Client website

https://example.com/client

### Open-source repository URL

https://github.com/example/client

### Compatibility

Compatible

### Targets

- [X] Android
- [ ] iOS
- [X] AndroidTV

### Price

Free and paid

### Download links

GitHub | https://github.com/example/client/releases
App Store | https://apps.apple.com/app/id123456
`

func TestSubmissionGeneratesClientCatalog(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("../../assets/clients/clients.yaml")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, "clients.yaml")
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(submission, filename, dir); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	updated, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config catalog
	if err := yaml.Unmarshal(updated, &config); err != nil {
		t.Fatal(err)
	}
	entry := config.Clients[len(config.Clients)-1]
	if entry.Name != "Example Client" || strings.Join(entry.Targets, ",") != "Android,AndroidTV" ||
		strings.Join(entry.Types, ",") != "Compatible" || !entry.Price.Free || !entry.Price.Paid ||
		len(entry.Downloads) != 2 || entry.Downloads[0].Text != "GitHub" {
		t.Fatalf("unexpected client: %#v", entry)
	}
	originalSuffix := string(data[strings.Index(string(data), "\ntargets:"):])
	if !strings.HasSuffix(string(updated), originalSuffix) {
		t.Fatal("insertion changed target or type configuration")
	}
	out := filepath.Join(dir, "CLIENTS.md")
	command := exec.Command("go", "run", "../generate-clients",
		"-clients", filename, "-clients-dir", dir,
		"-schema", "../../assets/clients/schema.json",
		"-template", "../../assets/clients/CLIENTS.md.tmpl", "-out", out)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate clients: %v\n%s", err, output)
	}
	rendered, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "Example Client") ||
		!strings.Contains(string(rendered), "https://apps.apple.com/app/id123456") {
		t.Fatalf("generated output does not contain client and downloads:\n%s", rendered)
	}
	if err := run(submission, filename, dir); err == nil || !strings.Contains(err.Error(), "already listed") {
		t.Fatalf("duplicate run() error = %v", err)
	}
}

func TestInvalidSubmissions(t *testing.T) {
	data, err := os.ReadFile("../../assets/clients/clients.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config catalog
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{"missing target", strings.ReplaceAll(strings.ReplaceAll(submission, "[X] Android\n", "[ ] Android\n"), "[X] AndroidTV", "[ ] AndroidTV"), "at least one target"},
		{"unknown target", strings.Replace(submission, "[X] AndroidTV", "[X] Unknown", 1), "unknown or duplicate target"},
		{"missing price", strings.Replace(submission, "Free and paid", "_No response_", 1), "price must"},
		{"invalid download", strings.Replace(submission, "GitHub | https://github.com/example/client/releases", "GitHub | javascript:alert(1)", 1), "download URL"},
		{"unsafe name", strings.Replace(submission, "Example Client", "Bad | Client", 1), "client name"},
		{"duplicate field", submission + "\n### Price\n\nPaid\n", "duplicate issue field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSubmission(test.body, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseSubmission() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDuplicateMaintainerEntry(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("../../assets/clients/clients.yaml")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(dir, "clients.yaml")
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatal(err)
	}
	extras := filepath.Join(dir, "extras")
	if err := os.Mkdir(extras, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extras, "mine.yaml"), []byte("clients:\n  - name: Different\n    website: https://example.com/client/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(submission, filename, extras); err == nil || !strings.Contains(err.Error(), "already listed") {
		t.Fatalf("run() error = %v, want duplicate URL", err)
	}
}
