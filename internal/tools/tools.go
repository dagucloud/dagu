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
	"unicode"

	"github.com/dagucloud/dagu/internal/core"
)

const (
	providerAqua     = "aqua"
	configFileName   = "aqua.yaml"
	checksumFileName = "aqua-checksums.json"
	manifestFileName = "manifest.json"

	// EnvManifest points command executors to the resolved Dagu tools manifest.
	EnvManifest = "DAGU_TOOLS_MANIFEST"
)

// Installer installs and resolves a DAG tool declaration.
type Installer interface {
	Install(ctx context.Context, cfg *core.ToolConfig, opts InstallOptions) (*Manifest, error)
}

// InstallOptions identifies the worker-local filesystem context for tools.
type InstallOptions struct {
	ToolsDir string
	// DataDir is kept as an internal fallback for callers that have not been
	// updated to pass ToolsDir directly.
	DataDir  string
	WorkDir  string
	Platform string
}

// CacheLayout contains worker-local cache paths for a toolset.
type CacheLayout struct {
	RootDir      string
	LockDir      string
	EnvDir       string
	BinDir       string
	ConfigFile   string
	ChecksumFile string
	ManifestFile string
}

// Manifest records the resolved commands made available to a DAG run.
type Manifest struct {
	Provider     string             `json:"provider"`
	Platform     string             `json:"platform"`
	Hash         string             `json:"hash"`
	RootDir      string             `json:"rootDir"`
	EnvDir       string             `json:"envDir"`
	BinDir       string             `json:"binDir"`
	Config       string             `json:"config"`
	Checksum     string             `json:"checksum"`
	ManifestFile string             `json:"manifestFile"`
	Commands     map[string]Command `json:"commands"`
}

// Command records one resolved executable path.
type Command struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Package string `json:"package"`
	Version string `json:"version"`
}

// CachePaths returns the worker-local cache paths for an aqua-backed toolset.
func CachePaths(toolsDir, platform, toolsetHash string) (CacheLayout, error) {
	toolsDir = strings.TrimSpace(toolsDir)
	if toolsDir == "" {
		return CacheLayout{}, fmt.Errorf("tools dir is required")
	}
	platform = sanitizePlatform(platform)
	if platform == "" {
		return CacheLayout{}, fmt.Errorf("platform is required")
	}
	toolsetHash = strings.TrimSpace(toolsetHash)
	if toolsetHash == "" {
		return CacheLayout{}, fmt.Errorf("toolset hash is required")
	}

	rootDir := filepath.Join(toolsDir, providerAqua, "root")
	lockDir := filepath.Join(toolsDir, providerAqua, "locks")
	envDir := filepath.Join(toolsDir, providerAqua, "envs", platform, toolsetHash)
	return CacheLayout{
		RootDir:      rootDir,
		LockDir:      lockDir,
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
		EnvManifest + "=" + manifestFilePath(manifest),
		"PATH=" + pathValue,
	}
}

func sanitizePlatform(platform string) string {
	platform = strings.TrimSpace(platform)
	return strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':':
			return '-'
		case unicode.IsSpace(r) || unicode.IsControl(r):
			return '-'
		default:
			return r
		}
	}, platform)
}

func manifestFilePath(manifest *Manifest) string {
	if strings.TrimSpace(manifest.ManifestFile) != "" {
		return manifest.ManifestFile
	}
	return filepath.Join(manifest.EnvDir, manifestFileName)
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
