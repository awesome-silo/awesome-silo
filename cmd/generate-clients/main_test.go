package main

import (
	"strings"
	"testing"

	generator "github.com/awesome-jellyfin/clients-md-generator"
)

func TestValidateReferencesRequiresOneCompatibilityType(t *testing.T) {
	tests := []struct {
		name      string
		types     []string
		wantError bool
	}{
		{name: "native", types: []string{"Native"}},
		{name: "compatible", types: []string{"Compatible"}},
		{name: "missing", wantError: true},
		{name: "both", types: []string{"Native", "Compatible"}, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := &generator.ClientsConfig{
				Clients: []*generator.Client{{
					Name:    "Example",
					Targets: []string{"Browser"},
					Types:   test.types,
				}},
				Targets: []*generator.TargetGroup{{
					Has: []*generator.Target{{Name: "Browser"}},
				}},
				Types: generator.ClientTypes{
					&generator.ClientType{Key: "Native"},
					&generator.ClientType{Key: "Compatible"},
				},
			}

			err := validateReferences(config)
			if (err != nil) != test.wantError {
				t.Fatalf("validateReferences() error = %v, wantError %v", err, test.wantError)
			}
			if test.wantError && !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("validateReferences() error = %v, want classification error", err)
			}
		})
	}
}

func TestRenderMarkdownDocumentPrioritizesClassificationByEnvironment(t *testing.T) {
	config := &generator.ClientsConfig{
		Clients: []*generator.Client{
			{Name: "Compatible App", Targets: []string{"Windows"}, Types: []string{"Compatible"}},
			{Name: "Native App", Targets: []string{"Windows"}, Types: []string{"Native"}},
		},
		Targets: []*generator.TargetGroup{
			{
				Display: "💻 Desktop",
				Has: []*generator.Target{
					{Name: "Windows", Mapped: "Windows"},
					{Name: "macOS", Mapped: "macOS"},
				},
			},
		},
		Types: generator.ClientTypes{
			&generator.ClientType{Key: "Native", Badge: "native", Section: true},
			&generator.ClientType{Key: "Compatible", Badge: "compatible", Section: true},
		},
	}

	got, err := renderMarkdownDocument(config)
	if err != nil {
		t.Fatalf("renderMarkdownDocument() error = %v", err)
	}

	nativeIndex := strings.Index(got, "# Native")
	compatibleIndex := strings.Index(got, "# Compatible")
	if nativeIndex < 0 || compatibleIndex < 0 || nativeIndex >= compatibleIndex {
		t.Fatalf("classification headings are missing or out of order:\n%s", got)
	}
	if !strings.Contains(got[nativeIndex:compatibleIndex], "## 💻 Desktop\n\n### Windows") {
		t.Fatalf("native clients are not grouped by environment and platform:\n%s", got)
	}
	if strings.Contains(got, "` native `") || strings.Contains(got, "` compatible `") {
		t.Fatalf("classification badges should not appear beside client names:\n%s", got)
	}
	if !strings.Contains(got, "| Name | Official | OSS | Free | Paid | Downloads |") {
		t.Fatalf("tables are missing the Official column:\n%s", got)
	}
	if strings.Index(got, "| Native App |") > strings.Index(got, "| Compatible App |") {
		t.Fatalf("native client table should be rendered first:\n%s", got)
	}
}

func TestValidateReferencesRejectsUnknownTargetAndType(t *testing.T) {
	tests := []struct {
		name    string
		targets []string
		types   []string
		want    string
	}{
		{name: "unknown target", targets: []string{"Console"}, types: []string{"Native"}, want: "undefined target"},
		{name: "unknown type", targets: []string{"Browser"}, types: []string{"Native", "Music"}, want: "undefined type"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := &generator.ClientsConfig{
				Clients: []*generator.Client{{
					Name:    "Example",
					Targets: test.targets,
					Types:   test.types,
				}},
				Targets: []*generator.TargetGroup{{
					Has: []*generator.Target{{Name: "Browser"}},
				}},
				Types: generator.ClientTypes{
					&generator.ClientType{Key: "Native"},
				},
			}

			err := validateReferences(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateReferences() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
