// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/google/jsonschema-go/jsonschema"
)

const (
	manifestFileName = "dagu-action.yaml"
	manifestVersion  = "dagu.dev/v1alpha1"
)

type manifest struct {
	APIVersion string         `yaml:"apiVersion"`
	Name       string         `yaml:"name"`
	DAG        string         `yaml:"dag"`
	Inputs     map[string]any `yaml:"inputs"`
	Outputs    map[string]any `yaml:"outputs"`
}

func loadManifest(rootDir string) (*manifest, error) {
	path := filepath.Join(rootDir, manifestFileName)
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read action manifest: %w", err)
	}
	if err := validateManifestKeys(data); err != nil {
		return nil, err
	}
	var m manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse action manifest: %w", err)
	}
	if err := m.validate(rootDir); err != nil {
		return nil, err
	}
	return &m, nil
}

func validateManifestKeys(data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse action manifest: %w", err)
	}
	if err := validateMapKeys("action manifest", raw, map[string]struct{}{
		"apiVersion": {},
		"name":       {},
		"dag":        {},
		"inputs":     {},
		"outputs":    {},
	}); err != nil {
		return err
	}
	return nil
}

func validateMapKeys(section string, raw map[string]any, allowed map[string]struct{}) error {
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s field %q is not supported", section, key)
		}
	}
	return nil
}

func (m *manifest) validate(rootDir string) error {
	if strings.TrimSpace(m.APIVersion) == "" {
		return fmt.Errorf("action manifest apiVersion is required")
	}
	if strings.TrimSpace(m.APIVersion) != manifestVersion {
		return fmt.Errorf("action manifest apiVersion must be %q", manifestVersion)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("action manifest name is required")
	}
	dagPath := strings.TrimSpace(m.DAG)
	if dagPath == "" {
		return fmt.Errorf("action dag is required")
	}
	resolved, err := safeRelativePath(rootDir, dagPath)
	if err != nil {
		return fmt.Errorf("invalid action dag: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("stat action dag: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("action dag must be a file")
	}
	return nil
}

func (m *manifest) validateInput(input map[string]any) error {
	if len(m.Inputs) == 0 {
		return nil
	}
	data, err := json.Marshal(m.Inputs)
	if err != nil {
		return fmt.Errorf("marshal action input schema: %w", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return fmt.Errorf("parse action input schema: %w", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return fmt.Errorf("resolve action input schema: %w", err)
	}
	if err := resolved.Validate(input); err != nil {
		return fmt.Errorf("action input does not match inputs schema: %w", err)
	}
	return nil
}

func (m *manifest) validateOutput(output any) error {
	if len(m.Outputs) == 0 {
		return nil
	}
	data, err := json.Marshal(m.Outputs)
	if err != nil {
		return fmt.Errorf("marshal action output schema: %w", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return fmt.Errorf("parse action output schema: %w", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return fmt.Errorf("resolve action output schema: %w", err)
	}
	if err := resolved.Validate(output); err != nil {
		return fmt.Errorf("action output does not match outputs schema: %w", err)
	}
	return nil
}

func isAbsoluteActionPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if filepath.IsAbs(value) {
		return true
	}
	slashPath := strings.ReplaceAll(value, `\`, "/")
	if path.IsAbs(slashPath) {
		return true
	}
	if len(slashPath) >= 2 && slashPath[1] == ':' {
		drive := slashPath[0]
		return ('A' <= drive && drive <= 'Z') || ('a' <= drive && drive <= 'z')
	}
	return false
}
