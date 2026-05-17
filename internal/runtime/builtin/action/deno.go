// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	cmnconfig "github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	daguaqua "github.com/dagucloud/dagu/internal/tools/aqua"
)

const (
	runtimeTypeDeno = "deno"
	denoCommand     = "deno"
	denoAquaPackage = "denoland/deno"
	envToolsDir     = "DAGU_TOOLS_DIR"
)

func resolveDeno(ctx context.Context, version string, env map[string]string, workDir string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return "", fmt.Errorf("deno version is required")
	}
	if path, ok := denoFromManifest(env[dagutools.EnvManifest], version); ok {
		return path, nil
	}
	toolsDir := actionToolsDir(ctx, env)
	if toolsDir == "" {
		return "", fmt.Errorf("tools dir is required to install Deno runtime")
	}
	manifest, err := daguaqua.New().Install(ctx, &core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Package:  denoAquaPackage,
			Version:  version,
			Commands: []string{denoCommand},
		}},
	}, dagutools.InstallOptions{
		ToolsDir: toolsDir,
		WorkDir:  workDir,
	})
	if err != nil {
		return "", err
	}
	cmd, ok := manifest.Commands[denoCommand]
	if !ok || strings.TrimSpace(cmd.Path) == "" {
		return "", fmt.Errorf("installed Deno runtime did not expose %q", denoCommand)
	}
	return cmd.Path, nil
}

func actionToolsDir(ctx context.Context, env map[string]string) string {
	if cfg := cmnconfig.GetConfig(ctx); cfg != nil {
		if toolsDir := strings.TrimSpace(cfg.Paths.ToolsDir); toolsDir != "" {
			return toolsDir
		}
	}
	if env == nil {
		return ""
	}
	if toolsDir := strings.TrimSpace(env[envToolsDir]); toolsDir != "" {
		return toolsDir
	}
	return inferToolsDirFromManifest(env[dagutools.EnvManifest])
}

func denoFromManifest(manifestPath, version string) (string, bool) {
	manifestPath = strings.TrimSpace(manifestPath)
	if manifestPath == "" {
		return "", false
	}
	manifest, err := dagutools.ReadManifest(manifestPath)
	if err != nil {
		return "", false
	}
	cmd, ok := manifest.Commands[denoCommand]
	if !ok || strings.TrimSpace(cmd.Path) == "" {
		return "", false
	}
	if strings.TrimSpace(cmd.Package) != denoAquaPackage || strings.TrimSpace(cmd.Version) != version {
		return "", false
	}
	return cmd.Path, true
}

func inferToolsDirFromManifest(manifestPath string) string {
	manifestPath = strings.TrimSpace(manifestPath)
	if manifestPath == "" {
		return ""
	}
	manifest, err := dagutools.ReadManifest(manifestPath)
	if err != nil || strings.TrimSpace(manifest.RootDir) == "" {
		return ""
	}
	root := filepath.Clean(manifest.RootDir)
	if filepath.Base(root) != "root" {
		return ""
	}
	aquaDir := filepath.Dir(root)
	if filepath.Base(aquaDir) != "aqua" {
		return ""
	}
	return filepath.Dir(aquaDir)
}
