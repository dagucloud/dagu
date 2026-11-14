// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	schemapkg "github.com/dagucloud/dagu/v2/internal/cmn/schema"
	"github.com/spf13/cobra"
)

// Schema creates the 'schema' CLI command that displays JSON schema
// documentation for DAG definitions, configuration, or calendar files.
func Schema() *cobra.Command {
	return &cobra.Command{
		Use:   "schema <dag|config|calendar> [path]",
		Short: "Display schema documentation for DAG, config, or calendar files",
		Long: `Browse the JSON schema for DAG definitions, configuration, or business
calendar files.

Available schemas: dag, config, calendar

Call without a path to see all root-level fields. Use a dot-separated
path to drill into nested sections.`,
		Example: `  dagu schema dag                  Show all DAG root-level fields
  dagu schema dag steps            Show step properties
  dagu schema dag steps.container  Show container configuration
  dagu schema config               Show all config root-level fields
  dagu schema config auth          Show authentication configuration
  dagu schema calendar holidays    Show the forms of a calendar holiday`,
		ValidArgs: []string{"dag", "config", "calendar"},
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
		"calendar": schemapkg.CalendarSchemaJSON,
		"config":   schemapkg.ConfigSchemaJSON,
		"dag":      schemapkg.DAGSchemaJSON,
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
		current = expandSchemaShape(root, current, map[string]bool{})
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
		next, ok := schemaChild(root, current, part, map[string]bool{})
		if !ok {
			return nil, fmt.Errorf("path %q not found", strings.Join(parts, "."))
		}
		current = next
	}
	return current, nil
}

// schemaUnionKeys name the keywords whose branches each describe a form a
// value may take.
var schemaUnionKeys = []string{"oneOf", "anyOf", "allOf"}

// schemaChild finds the schema of the named child of node, looking through
// references, union branches, and array items. seen holds the references
// already followed, so a recursive definition is not followed again.
func schemaChild(root, node any, name string, seen map[string]bool) (any, bool) {
	obj, ok := node.(map[string]any)
	if !ok {
		return nil, false
	}

	if ref, ok := obj["$ref"].(string); ok {
		if seen[ref] {
			return nil, false
		}
		seen[ref] = true
		target, ok := schemaRef(root, ref)
		if !ok {
			return nil, false
		}
		return schemaChild(root, target, name, seen)
	}
	if properties, ok := obj["properties"].(map[string]any); ok {
		if child, ok := properties[name]; ok {
			return child, true
		}
	}
	if items, ok := obj["items"]; ok {
		if child, ok := schemaChild(root, items, name, seen); ok {
			return child, true
		}
	}
	for _, key := range schemaUnionKeys {
		branches, _ := obj[key].([]any)
		for _, branch := range branches {
			if child, ok := schemaChild(root, branch, name, seen); ok {
				return child, true
			}
		}
	}
	if child, ok := obj[name]; ok {
		return child, true
	}
	return nil, false
}

// expandSchemaShape replaces the references that define the shape of node,
// in the node itself, its union branches, its array items, and its map
// values, with the definitions they name. Field schemas under properties
// keep their references. expanded holds the definitions already shown, so
// each is expanded once.
func expandSchemaShape(root, node any, expanded map[string]bool) any {
	obj, ok := node.(map[string]any)
	if !ok {
		return node
	}

	if ref, ok := obj["$ref"].(string); ok {
		if expanded[ref] {
			return node
		}
		target, ok := schemaRef(root, ref)
		targetObj, isObj := target.(map[string]any)
		if !ok || !isObj {
			return node
		}
		expanded[ref] = true
		merged := maps.Clone(targetObj)
		for key, value := range obj {
			if key != "$ref" {
				merged[key] = value
			}
		}
		return expandSchemaShape(root, merged, expanded)
	}

	out := maps.Clone(obj)
	for _, key := range schemaUnionKeys {
		branches, ok := obj[key].([]any)
		if !ok {
			continue
		}
		expandedBranches := make([]any, len(branches))
		for i, branch := range branches {
			expandedBranches[i] = expandSchemaShape(root, branch, expanded)
		}
		out[key] = expandedBranches
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if child, ok := obj[key].(map[string]any); ok {
			out[key] = expandSchemaShape(root, child, expanded)
		}
	}
	return out
}

// schemaRef resolves a local JSON pointer reference such as
// "#/definitions/step" against root.
func schemaRef(root any, ref string) (any, bool) {
	pointer, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil, false
	}
	node := root
	for token := range strings.SplitSeq(pointer, "/") {
		obj, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		if node, ok = obj[token]; !ok {
			return nil, false
		}
	}
	return node, true
}
