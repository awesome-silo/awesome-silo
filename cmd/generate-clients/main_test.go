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
