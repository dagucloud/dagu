// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	providerAqua     = "aqua"
	configFileName   = "aqua.yaml"
	checksumFileName = "aqua-checksums.json"
	manifestFileName = "manifest.json"

	maxLocalRegistryBytes = 16 << 20

	// EnvManifest points command executors to the resolved Dagu tools manifest.
	EnvManifest = "DAGU_TOOLS_MANIFEST"
)

// managedEnvKeys are the environment variable keys a resolved toolset injects
// into a run environment. Their values are local to the run's host and must
// not be forwarded to other runs.
var managedEnvKeys = []string{
	"PATH",
	"AQUA_ROOT_DIR",
	"AQUA_CONFIG",
	"AQUA_DISABLE_LAZY_INSTALL",
	"AQUA_CHECKSUM",
	"AQUA_REQUIRE_CHECKSUM",
	"AQUA_ENFORCE_CHECKSUM",
	"AQUA_ENFORCE_REQUIRE_CHECKSUM",
	EnvManifest,
}

// IsManagedEnvKey reports whether key is a tool-managed environment variable.
func IsManagedEnvKey(key string) bool {
	for _, candidate := range managedEnvKeys {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	return false
}

// Installer installs and resolves a DAG tool declaration.
type Installer interface {
	Install(ctx context.Context, cfg *ir.ToolConfig, opts InstallOptions) (*Manifest, error)
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

// ToolsetHash returns a stable hash and, for local registries, the bounded
// snapshot that the installer must use instead of reopening the source path.
func ToolsetHash(cfg *ir.ToolConfig, platform string) (string, []byte, error) {
	if cfg == nil {
		return "", nil, fmt.Errorf("tools config is required")
	}
	payload := struct {
		Platform                    string         `json:"platform"`
		Tools                       *ir.ToolConfig `json:"tools"`
		LocalRegistrySnapshotDigest string         `json:"localRegistrySnapshotDigest,omitempty"`
	}{
		Platform: platform,
		Tools:    cfg,
	}
	var snapshot []byte
	if cfg.Registry != nil && strings.TrimSpace(cfg.Registry.Type) == "local" {
		path := strings.TrimSpace(cfg.Registry.Path)
		if err := ir.ValidateWorkerAbsPath(path); err != nil {
			return "", nil, fmt.Errorf("registry.path: %w", err)
		}
		data, err := readLocalRegistry(path)
		if err != nil {
			return "", nil, fmt.Errorf("read local registry: %w", err)
		}
		snapshot = data
		digest := sha256.Sum256(snapshot)
		payload.LocalRegistrySnapshotDigest = hex.EncodeToString(digest[:])
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", nil, fmt.Errorf("marshal toolset: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), snapshot, nil
}

func readLocalRegistry(path string) ([]byte, error) {
	file, err := openLocalRegistry(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local registry %q is not a regular file", path)
	}
	if info.Size() > maxLocalRegistryBytes {
		return nil, fmt.Errorf("local registry %q exceeds %d bytes", path, maxLocalRegistryBytes)
	}
	// Read one extra byte to detect a file that grows past the limit after stat.
	data, err := io.ReadAll(io.LimitReader(file, maxLocalRegistryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLocalRegistryBytes {
		return nil, fmt.Errorf("local registry %q exceeds %d bytes", path, maxLocalRegistryBytes)
	}
	return data, nil
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
