// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
)

// UnmarshalYAML accepts both the full tools object and the shorthand package list.
func (t *toolsConfig) UnmarshalYAML(data []byte) error {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal tools: %w", err)
	}

	switch raw.(type) {
	case nil:
		*t = toolsConfig{}
		return nil
	case []any, []string:
		var packages []toolPackage
		if err := yaml.Unmarshal(data, &packages); err != nil {
			return err
		}
		*t = toolsConfig{Packages: packages}
		return nil
	case map[string]any:
		type alias toolsConfig
		var decoded alias
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("unmarshal tools object: %w", err)
		}
		*t = toolsConfig(decoded)
		return nil
	default:
		return fmt.Errorf("tools must be an object or package shorthand list, got %T", raw)
	}
}

// UnmarshalYAML accepts either the full package object or "package@version".
func (p *toolPackage) UnmarshalYAML(data []byte) error {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal tool package: %w", err)
	}

	switch raw := raw.(type) {
	case nil:
		*p = toolPackage{}
		return nil
	case string:
		pkg, err := parseToolPackageShorthand(raw)
		if err != nil {
			return err
		}
		*p = pkg
		return nil
	case map[string]any:
		type alias toolPackage
		var decoded alias
		if err := yaml.Unmarshal(data, &decoded); err != nil {
			return fmt.Errorf("unmarshal tool package object: %w", err)
		}
		*p = toolPackage(decoded)
		return nil
	default:
		return fmt.Errorf("tool package must be an object or package@version string, got %T", raw)
	}
}

func parseToolPackageShorthand(ref string) (toolPackage, error) {
	ref = strings.TrimSpace(ref)
	if strings.IndexFunc(ref, unicode.IsSpace) >= 0 || strings.Count(ref, "@") != 1 {
		return toolPackage{}, fmt.Errorf(`tool package shorthand must be "package@version", got %q`, ref)
	}
	idx := strings.LastIndex(ref, "@")
	if idx <= 0 || idx == len(ref)-1 {
		return toolPackage{}, fmt.Errorf(`tool package shorthand must be "package@version", got %q`, ref)
	}
	return toolPackage{
		Package: strings.TrimSpace(ref[:idx]),
		Version: strings.TrimSpace(ref[idx+1:]),
	}, nil
}
