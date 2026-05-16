// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/google/jsonschema-go/jsonschema"
)

const manifestFileName = "dagu-action.yaml"

var envNameRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type manifest struct {
	APIVersion  string             `yaml:"apiVersion"`
	Name        string             `yaml:"name"`
	Runtime     runtimeManifest    `yaml:"runtime"`
	Inputs      map[string]any     `yaml:"inputs"`
	Outputs     map[string]any     `yaml:"outputs"`
	Permissions permissionManifest `yaml:"permissions"`
}

type runtimeManifest struct {
	Type       string `yaml:"type"`
	Deno       string `yaml:"deno"`
	Entrypoint string `yaml:"entrypoint"`
}

type permissionManifest struct {
	Net   []string `yaml:"net"`
	Env   []string `yaml:"env"`
	Read  []string `yaml:"read"`
	Write []string `yaml:"write"`
}

func loadManifest(rootDir string) (*manifest, error) {
	path := filepath.Join(rootDir, manifestFileName)
	data, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read action manifest: %w", err)
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

func (m *manifest) validate(rootDir string) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("action manifest name is required")
	}
	if strings.TrimSpace(m.Runtime.Type) != runtimeTypeDeno {
		return fmt.Errorf("action runtime type must be %q", runtimeTypeDeno)
	}
	if strings.TrimSpace(m.Runtime.Deno) == "" {
		return fmt.Errorf("action runtime.deno is required")
	}
	entrypoint := strings.TrimSpace(m.Runtime.Entrypoint)
	if entrypoint == "" {
		return fmt.Errorf("action runtime.entrypoint is required")
	}
	entrypointPath, err := safeRelativePath(rootDir, entrypoint)
	if err != nil {
		return fmt.Errorf("invalid action runtime.entrypoint: %w", err)
	}
	info, err := os.Stat(entrypointPath)
	if err != nil {
		return fmt.Errorf("stat action runtime.entrypoint: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("action runtime.entrypoint must be a file")
	}
	for _, host := range m.Permissions.Net {
		if err := validateNetPermission(host); err != nil {
			return err
		}
	}
	for _, env := range m.Permissions.Env {
		if !envNameRegexp.MatchString(strings.TrimSpace(env)) {
			return fmt.Errorf("invalid action env permission %q", env)
		}
	}
	for _, path := range m.Permissions.Read {
		if err := validateRelativePermissionPath(rootDir, path, "read"); err != nil {
			return err
		}
	}
	for _, path := range m.Permissions.Write {
		if err := validateRelativePermissionPath(rootDir, path, "write"); err != nil {
			return err
		}
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

func validateNetPermission(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty action net permission")
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, `/\`) || strings.Contains(host, "*") {
		return fmt.Errorf("invalid action net permission %q", host)
	}
	return nil
}

func validateRelativePermissionPath(rootDir, path string, kind string) error {
	path = strings.TrimSpace(path)
	if path == "" || isAbsoluteActionPath(path) {
		return fmt.Errorf("invalid action %s permission path %q", kind, path)
	}
	if _, err := safeRelativePath(rootDir, path); err != nil {
		return fmt.Errorf("invalid action %s permission path %q: %w", kind, path, err)
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
