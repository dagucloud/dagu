// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package command

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/runtime"
	dagutools "github.com/dagucloud/dagu/internal/tools"
)

func resolveRuntimeToolCommand(ctx context.Context, command string) string {
	if strings.TrimSpace(command) == "" || filepath.IsAbs(command) || strings.ContainsAny(command, `/\`) {
		return command
	}
	manifestPath := runtime.GetEnv(ctx).UserEnvsMap()[dagutools.EnvManifest]
	resolved, ok := resolveDeclaredToolCommand(manifestPath, command)
	if !ok {
		return command
	}
	return resolved
}

func resolveDeclaredToolCommand(manifestPath, command string) (string, bool) {
	if manifestPath == "" || command == "" {
		return "", false
	}
	manifest, err := dagutools.ReadManifest(manifestPath)
	if err != nil {
		return "", false
	}
	cmd, ok := manifest.Commands[command]
	if !ok || cmd.Path == "" {
		return "", false
	}
	return cmd.Path, true
}
