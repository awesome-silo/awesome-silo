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

	body, err := renderMarkdownDocument(config)
	if err != nil {
		return fmt.Errorf("render client tables: %w", err)
	}

	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		return fmt.Errorf("parse %s: %w", templateFile, err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, pageData{Body: body}); err != nil {
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

func renderMarkdownDocument(config *generator.ClientsConfig) (string, error) {
	targetClients := make(map[string][]*generator.Client)
	for _, client := range config.Clients {
		for _, target := range client.Targets {
			key := normalize(target)
			targetClients[key] = append(targetClients[key], client)
		}
	}

	var document generator.Vertical
	for _, typeKey := range []string{"Native", "Compatible"} {
		document.Append(generator.NewHeading(1, generator.NewText(typeKey)))
		for _, targetGroup := range config.Targets {
			group := generator.NewVertical()
			hasMultipleTargets := len(targetGroup.Has) > 1

			for _, target := range targetGroup.Has {
				var clients []*generator.Client
				for _, client := range targetClients[normalize(target.Name)] {
					if hasType(client, typeKey) {
						clients = append(clients, client)
					}
				}
				if len(clients) == 0 {
					continue
				}

				if hasMultipleTargets {
					group.Append(generator.NewHeading(3, generator.NewText(target.Mapped)))
				}
				table, err := printClientTableWithoutClassificationBadges(clients, config)
				if err != nil {
					return "", err
				}
				group.Append(table)
			}

			if len(group) > 0 {
				document.Append(generator.NewHeading(2, generator.NewText(targetGroup.Display)))
				document.Append(&group)
			}
		}
	}

	var extraTypes generator.Vertical
	for _, clientType := range config.Types {
		if !clientType.Section || clientType.Key == "Native" || clientType.Key == "Compatible" {
			continue
		}

		var clients []*generator.Client
		for _, client := range config.Clients {
			if hasType(client, clientType.Key) {
				clients = append(clients, client)
			}
		}
		if len(clients) == 0 {
			continue
		}
		generator.SortClientsByName(clients)
		table, err := printClientTableWithoutClassificationBadges(clients, config)
		if err != nil {
			return "", err
		}

		if len(extraTypes) == 0 {
			extraTypes.Append(generator.NewHeading(1, generator.NewText("By Type")))
		}
		extraTypes.Append(generator.NewHeading(2, generator.NewText(clientType.StringWithBadge())))
		extraTypes.Append(table)
	}
	document.Append(extraTypes...)

	var badgeLegend generator.Vertical
	for _, clientType := range config.Types {
		if clientType.Badge == "" || clientType.Key == "Native" || clientType.Key == "Compatible" || clientType.Key == generator.OfficialTypeKey {
			continue
		}
		badgeLegend.Append(generator.NewHorizontal(
			generator.NewText(clientType.String(), generator.Bold),
			generator.NewCode(clientType.Badge, generator.CodePadded),
		))
	}
	if len(badgeLegend) > 0 {
		document.Append(generator.HorizontalDivider{})
		list := generator.NewList(false)
		list.Append(badgeLegend...)
		document.Append(list)
	}

	return document.Render(), nil
}

func printClientTableWithoutClassificationBadges(
	clients []*generator.Client,
	config *generator.ClientsConfig,
) (generator.MarkdownRenderer, error) {
	sorted := append([]*generator.Client(nil), clients...)
	generator.SortClientsByName(sorted)

	builder := generator.NewTableBuilder(
		generator.NewText("Name"),
		generator.NewText("Official"),
		generator.NewText("OSS"),
		generator.NewText("Free"),
		generator.NewText("Paid"),
		generator.NewText("Downloads"),
	)

	for _, client := range sorted {
		official := generator.DerefDef(client.Official, strings.HasPrefix(client.OpenSourceURL, generator.JellyfinOrgURL))
		free := generator.DerefDef(client.Price.Free, client.OpenSourceURL != "")
		paid := generator.DerefDef(client.Price.Paid, false)

		name := generator.NewHorizontal(generator.NewText(client.Name))
		if generator.Deref(client.Beta) {
			if betaType, ok := config.Types.FindType(generator.BetaTypeKey); ok && betaType.Badge != "" {
				name.Append(generator.NewCode(betaType.Badge, generator.CodePadded))
			}
		}
		for _, clientType := range client.Types {
			if clientType == "Native" || clientType == "Compatible" {
				continue
			}
			if t, ok := config.Types.FindType(clientType); ok && t.Badge != "" {
				name.Append(generator.NewCode(t.Badge, generator.CodePadded))
			}
		}

		var nameElement generator.MarkdownRenderer = name
		if url := generator.Select(client.Website != "", client.Website, client.OpenSourceURL); url != "" {
			nameElement = &generator.Link{Text: name, URL: url}
		}

		downloads := generator.NewHorizontal()
		for _, download := range client.Downloads {
			downloads.Append(download.Render())
		}

		builder.AddRow(
			nameElement,
			generator.NewText(generator.Select(official, generator.GoodTrue, generator.BadFalse)),
			generator.NewText(generator.Select(client.OpenSourceURL != "", generator.GoodTrue, generator.BadFalse)),
			generator.NewText(generator.Select(free, generator.GoodTrue, generator.BadFalse)),
			generator.NewText(generator.Select(paid, generator.BadTrue, generator.GoodFalse)),
			downloads,
		)
	}
	return builder.Build(), nil
}

func hasType(client *generator.Client, typeKey string) bool {
	for _, clientType := range client.Types {
		if clientType == typeKey {
			return true
		}
	}
	return false
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
