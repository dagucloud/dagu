// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	daguaqua "github.com/dagucloud/dagu/internal/tools/aqua"
)

const (
	runtimeTypeDeno = "deno"
	denoCommand     = "deno"
	denoAquaPackage = "denoland/deno"
)

func resolveDeno(ctx context.Context, version string, env map[string]string, workDir string) (string, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return "", fmt.Errorf("deno version is required")
	}
	if path, ok := denoFromManifest(env[dagutools.EnvManifest], version); ok {
		return path, nil
	}
	toolsDir := strings.TrimSpace(env[dagutools.EnvToolsDir])
	if toolsDir == "" {
		toolsDir = inferToolsDirFromManifest(env[dagutools.EnvManifest])
	}
	if toolsDir == "" {
		return "", fmt.Errorf("%s is required to install Deno runtime", dagutools.EnvToolsDir)
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
