package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	generator "github.com/awesome-jellyfin/clients-md-generator"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

type pageData struct {
	Body string
}

func main() {
	clientsFile := flag.String("clients", "assets/clients/clients.yaml", "path to clients.yaml")
	clientsDir := flag.String("clients-dir", "assets/clients/clients.d", "directory of maintainer-owned client YAML files")
	schemaFile := flag.String("schema", "assets/clients/schema.json", "path to the client JSON schema")
	templateFile := flag.String("template", "assets/clients/CLIENTS.md.tmpl", "path to the Markdown page template")
	outputFile := flag.String("out", "CLIENTS.md", "output path")
	check := flag.Bool("check", false, "fail if the output file is not up to date")
	flag.Parse()

	if err := run(*clientsFile, *clientsDir, *schemaFile, *templateFile, *outputFile, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(clientsFile, clientsDir, schemaFile, templateFile, outputFile string, check bool) error {
	schema, err := jsonschema.Compile(schemaFile)
	if err != nil {
		return fmt.Errorf("compile client schema: %w", err)
	}

	if err := validateYAML(schema, clientsFile); err != nil {
		return err
	}
	config, err := generator.LoadConfig(clientsFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", clientsFile, err)
	}

	paths, err := filepath.Glob(filepath.Join(clientsDir, "*.yaml"))
	if err != nil {
		return fmt.Errorf("find client files: %w", err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := validateYAML(schema, path); err != nil {
			return err
		}
		extra, err := generator.LoadConfig(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}
		config.Clients = append(config.Clients, extra.Clients...)
	}

	if err := validateReferences(config); err != nil {
		return err
	}

	var body bytes.Buffer
	if err := generator.CreateMarkdownDocument(&body, config); err != nil {
		return fmt.Errorf("render client tables: %w", err)
	}

	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		return fmt.Errorf("parse %s: %w", templateFile, err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, pageData{Body: body.String()}); err != nil {
		return fmt.Errorf("render %s: %w", templateFile, err)
	}

	if check {
		current, err := os.ReadFile(outputFile)
		if err != nil {
			return fmt.Errorf("read generated file %s: %w", outputFile, err)
		}
		if !bytes.Equal(current, output.Bytes()) {
			return fmt.Errorf("%s is out of date; run `go run ./cmd/generate-clients`", outputFile)
		}
		fmt.Printf("%s is up to date\n", outputFile)
		return nil
	}

	if err := os.WriteFile(outputFile, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write generated file %s: %w", outputFile, err)
	}
	fmt.Printf("Generated %s from %s and %d files in %s\n", outputFile, clientsFile, len(paths), clientsDir)
	return nil
}

func validateYAML(schema *jsonschema.Schema, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var document any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse YAML %s: %w", path, err)
	}
	jsonDocument, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("convert %s to JSON for schema validation: %w", path, err)
	}
	var value any
	if err := json.Unmarshal(jsonDocument, &value); err != nil {
		return fmt.Errorf("decode JSON for %s: %w", path, err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate %s: %w", path, err)
	}
	return nil
}

func validateReferences(config *generator.ClientsConfig) error {
	targets := make(map[string]struct{})
	for _, group := range config.Targets {
		for _, target := range group.Has {
			targets[normalize(target.Name)] = struct{}{}
		}
	}
	types := make(map[string]struct{})
	for _, clientType := range config.Types {
		types[clientType.Key] = struct{}{}
	}

	for _, client := range config.Clients {
		for _, target := range client.Targets {
			if _, ok := targets[normalize(target)]; !ok {
				return fmt.Errorf("client %q references undefined target %q", client.Name, target)
			}
		}
		nativeOrCompatible := 0
		for _, clientType := range client.Types {
			if _, ok := types[clientType]; !ok {
				return fmt.Errorf("client %q references undefined type %q", client.Name, clientType)
			}
			if clientType == "Native" || clientType == "Compatible" {
				nativeOrCompatible++
			}
		}
		if nativeOrCompatible != 1 {
			return fmt.Errorf("client %q must have exactly one of the Native or Compatible types", client.Name)
		}
	}
	return nil
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
