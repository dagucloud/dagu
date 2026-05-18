// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"fmt"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/goccy/go-yaml"
)

const (
	providerAqua = "aqua"
)

func effectiveToolConfig(cfg *core.ToolConfig) *core.ToolConfig {
	if cfg == nil {
		return nil
	}
	effective := *cfg
	effective.Packages = make([]core.ToolPackage, len(cfg.Packages))
	for i, pkg := range cfg.Packages {
		pkg.Commands = append([]string{}, pkg.Commands...)
		effective.Packages[i] = pkg
	}
	if cfg.Registry != nil {
		registry := *cfg.Registry
		if emptyDefault(strings.TrimSpace(registry.Type), "standard") == "standard" && strings.TrimSpace(registry.Ref) == "" {
			registry.Ref = core.DefaultAquaStandardRegistryRef
		}
		effective.Registry = &registry
		return &effective
	}
	effective.Registry = &core.ToolRegistry{
		Name: "standard",
		Type: "standard",
		Ref:  core.DefaultAquaStandardRegistryRef,
	}
	return &effective
}

type configFile struct {
	Checksum   checksumEntry   `yaml:"checksum"`
	Registries []registryEntry `yaml:"registries"`
	Packages   []packageEntry  `yaml:"packages"`
}

type checksumEntry struct {
	Enabled         bool     `yaml:"enabled"`
	RequireChecksum bool     `yaml:"require_checksum"`
	SupportedEnvs   []string `yaml:"supported_envs,omitempty"`
}

type registryEntry struct {
	Name      string `yaml:"name,omitempty"`
	Type      string `yaml:"type,omitempty"`
	RepoOwner string `yaml:"repo_owner,omitempty"`
	RepoName  string `yaml:"repo_name,omitempty"`
	Ref       string `yaml:"ref,omitempty"`
	Path      string `yaml:"path,omitempty"`
}

type packageEntry struct {
	Name     string `yaml:"name"`
	Registry string `yaml:"registry,omitempty"`
	Version  string `yaml:"version"`
}

// RenderConfig renders a Dagu tool declaration as an aqua.yaml file.
func RenderConfig(cfg *core.ToolConfig) ([]byte, error) {
	return RenderConfigForPlatform(cfg, "")
}

// RenderConfigForPlatform renders a Dagu tool declaration for a worker platform.
func RenderConfigForPlatform(cfg *core.ToolConfig, platform string) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("tools config is required")
	}
	if cfg.Provider != "" && cfg.Provider != providerAqua {
		return nil, fmt.Errorf("unsupported tools provider %q", cfg.Provider)
	}
	if len(cfg.Packages) == 0 {
		return nil, fmt.Errorf("packages is required")
	}

	registry, err := renderRegistry(cfg.Registry)
	if err != nil {
		return nil, err
	}
	checksum := checksumEntry{
		Enabled:         true,
		RequireChecksum: true,
	}
	if platform = strings.TrimSpace(platform); platform != "" {
		checksum.SupportedEnvs = []string{platform}
	}

	file := configFile{
		Checksum:   checksum,
		Registries: []registryEntry{registry},
		Packages:   make([]packageEntry, 0, len(cfg.Packages)),
	}
	for _, pkg := range cfg.Packages {
		if strings.TrimSpace(pkg.Package) == "" {
			return nil, fmt.Errorf("package is required")
		}
		if strings.TrimSpace(pkg.Version) == "" {
			return nil, fmt.Errorf("version is required")
		}
		file.Packages = append(file.Packages, packageEntry{
			Name:     strings.TrimSpace(pkg.Package),
			Registry: emptyDefault(strings.TrimSpace(pkg.Registry), registry.Name),
			Version:  strings.TrimSpace(pkg.Version),
		})
	}
	return yaml.Marshal(file)
}

func renderRegistry(registry *core.ToolRegistry) (registryEntry, error) {
	if registry == nil {
		return registryEntry{
			Name: "standard",
			Type: "standard",
			Ref:  core.DefaultAquaStandardRegistryRef,
		}, nil
	}
	typ := emptyDefault(strings.TrimSpace(registry.Type), "standard")
	ref := strings.TrimSpace(registry.Ref)
	switch typ {
	case "standard":
		if ref == "" {
			ref = core.DefaultAquaStandardRegistryRef
		}
		return registryEntry{
			Name: emptyDefault(strings.TrimSpace(registry.Name), "standard"),
			Type: typ,
			Ref:  ref,
		}, nil
	case "github_content":
		if ref == "" {
			return registryEntry{}, fmt.Errorf("registry.ref is required")
		}
		repoOwner := strings.TrimSpace(registry.RepoOwner)
		repoName := strings.TrimSpace(registry.RepoName)
		path := strings.TrimSpace(registry.Path)
		if repoOwner == "" {
			return registryEntry{}, fmt.Errorf("registry.repo_owner is required")
		}
		if repoName == "" {
			return registryEntry{}, fmt.Errorf("registry.repo_name is required")
		}
		if path == "" {
			return registryEntry{}, fmt.Errorf("registry.path is required")
		}
		return registryEntry{
			Name:      emptyDefault(strings.TrimSpace(registry.Name), "standard"),
			Type:      typ,
			RepoOwner: repoOwner,
			RepoName:  repoName,
			Ref:       ref,
			Path:      path,
		}, nil
	default:
		return registryEntry{}, fmt.Errorf("unsupported tools registry type %q", typ)
	}
}

func emptyDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}
