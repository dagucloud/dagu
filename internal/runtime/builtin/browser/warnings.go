// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// addressFormats are the string formats for which the browser runtime gives
// an extract field the address an element points to. A string field without
// one gets the element's id, such as "0-11".
var addressFormats = []string{"uri", "uri-reference"}

// addressNameSuffixes end field names that suggest the field holds an
// address, such as url, detail_url, permalink, or hrefs.
var addressNameSuffixes = []string{"url", "uri", "link", "href"}

// configWarnings reports the extract warnings of a step's operations.
func configWarnings(step ir.Step) []string {
	cfg, err := parseConfig(step.ExecutorConfig.Config)
	if err != nil {
		return nil
	}
	var warnings []string
	for i, op := range cfg.Do {
		if op.Extract == nil {
			continue
		}
		for _, warning := range extractWarnings(*op.Extract) {
			warnings = append(warnings, fmt.Sprintf("do[%d]: %s", i, warning))
		}
	}
	return warnings
}

// extractWarnings reports fields whose names suggest a link's address but
// whose schema has no address format, since such a field silently gets the
// link's element id.
func extractWarnings(spec extractSpec) []string {
	var warnings []string
	for _, field := range addressFieldsWithoutFormat(spec.Schema) {
		warnings = append(warnings, fmt.Sprintf(
			"extract field %q has no format: uri, so a link's address comes back as its element id, such as \"0-11\"; add format: uri to the field",
			field))
	}
	return warnings
}

// addressFieldsWithoutFormat returns the paths of the string fields of schema
// whose names suggest an address and that have no address format. A path
// joins property names with dots and marks array items with [].
func addressFieldsWithoutFormat(schema map[string]any) []string {
	var paths []string
	var walk func(node map[string]any, name, path string)
	walk = func(node map[string]any, name, path string) {
		if isStringSchema(node) {
			format, _ := node["format"].(string)
			if namesAddress(name) && !slices.Contains(addressFormats, format) {
				paths = append(paths, path)
			}
			return
		}
		if items, ok := node["items"].(map[string]any); ok {
			walk(items, name, path+"[]")
		}
		properties, _ := node["properties"].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(properties)) {
			child, ok := properties[key].(map[string]any)
			if !ok {
				continue
			}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			walk(child, key, childPath)
		}
	}
	walk(schema, "", "")
	return paths
}

// isStringSchema reports whether a schema's type is string or a list of types
// that includes string.
func isStringSchema(schema map[string]any) bool {
	switch typ := schema["type"].(type) {
	case string:
		return typ == "string"
	case []any:
		return slices.Contains(typ, any("string"))
	default:
		return false
	}
}

// namesAddress reports whether a field name, in singular or plural, ends in
// one of addressNameSuffixes.
func namesAddress(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), "s")
	return slices.ContainsFunc(addressNameSuffixes, func(suffix string) bool {
		return strings.HasSuffix(name, suffix)
	})
}
