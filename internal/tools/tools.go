// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
)

const (
	providerAqua      = "aqua"
	configFileName    = "aqua.yaml"
	checksumFileName  = "aqua-checksums.json"
	manifestFileName  = "manifest.json"
	toolCacheRootPath = "tools"

	// EnvManifest points command executors to the resolved Dagu tools manifest.
	EnvManifest = "DAGU_TOOLS_MANIFEST"
)

// Installer installs and resolves a DAG tool declaration.
type Installer interface {
	Install(ctx context.Context, cfg *core.ToolConfig, opts InstallOptions) (*Manifest, error)
}

// InstallOptions identifies the worker-local filesystem context for tools.
type InstallOptions struct {
	DataDir  string
	WorkDir  string
	Platform string
}

// CacheLayout contains worker-local cache paths for a toolset.
type CacheLayout struct {
	RootDir      string
	EnvDir       string
	BinDir       string
	ConfigFile   string
	ChecksumFile string
	ManifestFile string
}

// Manifest records the resolved commands made available to a DAG run.
type Manifest struct {
	Provider string             `json:"provider"`
	Platform string             `json:"platform"`
	Hash     string             `json:"hash"`
	RootDir  string             `json:"rootDir"`
	EnvDir   string             `json:"envDir"`
	BinDir   string             `json:"binDir"`
	Config   string             `json:"config"`
	Checksum string             `json:"checksum"`
	Commands map[string]Command `json:"commands"`
}

// Command records one resolved executable path.
type Command struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Package string `json:"package"`
	Version string `json:"version"`
}

// CachePaths returns the worker-local cache paths for an aqua-backed toolset.
func CachePaths(dataDir, platform, toolsetHash string) (CacheLayout, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return CacheLayout{}, fmt.Errorf("data dir is required")
	}
	platform = strings.ReplaceAll(strings.TrimSpace(platform), "/", "-")
	if platform == "" {
		return CacheLayout{}, fmt.Errorf("platform is required")
	}
	toolsetHash = strings.TrimSpace(toolsetHash)
	if toolsetHash == "" {
		return CacheLayout{}, fmt.Errorf("toolset hash is required")
	}

	rootDir := filepath.Join(dataDir, toolCacheRootPath, providerAqua, "root")
	envDir := filepath.Join(dataDir, toolCacheRootPath, providerAqua, "envs", platform, toolsetHash)
	return CacheLayout{
		RootDir:      rootDir,
		EnvDir:       envDir,
		BinDir:       filepath.Join(envDir, "bin"),
		ConfigFile:   filepath.Join(envDir, configFileName),
		ChecksumFile: filepath.Join(envDir, checksumFileName),
		ManifestFile: filepath.Join(envDir, manifestFileName),
	}, nil
}

// ToolsetHash returns a stable hash for a tool declaration on a platform.
func ToolsetHash(cfg *core.ToolConfig, platform string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("tools config is required")
	}
	payload := struct {
		Platform string           `json:"platform"`
		Tools    *core.ToolConfig `json:"tools"`
	}{
		Platform: platform,
		Tools:    cfg,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal toolset: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// EnvVars returns the runtime env vars needed to expose a resolved aqua toolset.
func EnvVars(manifest *Manifest, basePath string) []string {
	if manifest == nil {
		return nil
	}
	pathValue := manifest.BinDir
	if basePath != "" {
		pathValue += string(os.PathListSeparator) + basePath
	}
	return []string{
		"AQUA_ROOT_DIR=" + manifest.RootDir,
		"AQUA_CONFIG=" + manifest.Config,
		"AQUA_DISABLE_LAZY_INSTALL=true",
		"AQUA_CHECKSUM=true",
		"AQUA_REQUIRE_CHECKSUM=true",
		"AQUA_ENFORCE_CHECKSUM=true",
		"AQUA_ENFORCE_REQUIRE_CHECKSUM=true",
		EnvManifest + "=" + filepath.Join(manifest.EnvDir, manifestFileName),
		"PATH=" + pathValue,
	}
}

// ReadManifest reads a resolved tools manifest from disk.
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read tools manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse tools manifest: %w", err)
	}
	return &manifest, nil
}
