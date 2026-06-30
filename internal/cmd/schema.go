// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	schemapkg "github.com/dagucloud/dagu/internal/cmn/schema"
	"github.com/spf13/cobra"
)

// Schema creates the 'schema' CLI command that displays JSON schema
// documentation for DAG definitions or configuration.
func Schema() *cobra.Command {
	return &cobra.Command{
		Use:   "schema <dag|config> [path]",
		Short: "Display schema documentation for DAG or config",
		Long: `Browse the JSON schema for DAG definitions or configuration.

Available schemas: dag, config

Call without a path to see all root-level fields. Use a dot-separated
path to drill into nested sections.`,
		Example: `  dagu schema dag                  Show all DAG root-level fields
  dagu schema dag steps            Show step properties
  dagu schema dag steps.container  Show container configuration
  dagu schema config               Show all config root-level fields
  dagu schema config server        Show server configuration`,
		ValidArgs: []string{"dag", "config"},
		Args:      cobra.RangeArgs(1, 2),
		RunE:      runSchema,
	}
}

func runSchema(cmd *cobra.Command, args []string) error {
	schemaName := args[0]
	if schemaName == "help" {
		return cmd.Help()
	}
	var path string
	if len(args) > 1 {
		path = args[1]
	}

	result, err := navigateSchema(schemaName, path)
	if err != nil {
		return fmt.Errorf("schema navigation failed: %w", err)
	}

	_, _ = fmt.Fprint(cmd.OutOrStdout(), result)
	return nil
}

func navigateSchema(schemaName, path string) (string, error) {
	schemas := map[string][]byte{
		"config": schemapkg.ConfigSchemaJSON,
		"dag":    schemapkg.DAGSchemaJSON,
	}
	data, ok := schemas[schemaName]
	if !ok {
		names := make([]string, 0, len(schemas))
		for name := range schemas {
			names = append(names, name)
		}
		sort.Strings(names)
		return "", fmt.Errorf("unknown schema %q; available schemas: %s", schemaName, strings.Join(names, ", "))
	}

	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return "", err
	}

	current := root
	if path != "" {
		var err error
		current, err = navigateSchemaPath(root, strings.Split(path, "."))
		if err != nil {
			return "", err
		}
	}

	result, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return "", err
	}
	return string(result) + "\n", nil
}

func navigateSchemaPath(root any, parts []string) (any, error) {
	current := root
	for _, part := range parts {
		next, ok := schemaChild(current, part)
		if !ok {
			return nil, fmt.Errorf("path %q not found", strings.Join(parts, "."))
		}
		current = next
	}
	return current, nil
}

func schemaChild(node any, name string) (any, bool) {
	obj, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}

	if properties, ok := obj["properties"].(map[string]any); ok {
		if child, ok := properties[name]; ok {
			return child, true
		}
	}
	if items, ok := obj["items"]; ok {
		if child, ok := schemaChild(items, name); ok {
			return child, true
		}
	}
	if child, ok := obj[name]; ok {
		return child, true
	}
	return nil, false
}
