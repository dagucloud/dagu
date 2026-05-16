// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/internal/core/exec"
)

func artifactOutputFilePath(ctx context.Context, raw string) (string, error) {
	artifactDir := ""
	if scope := GetEnv(ctx).Scope; scope != nil {
		artifactDir, _ = scope.Get(exec.EnvKeyDAGRunArtifactsDir)
	}
	if strings.TrimSpace(artifactDir) == "" {
		return "", fmt.Errorf("%s is not set; enable artifacts for this DAG", exec.EnvKeyDAGRunArtifactsDir)
	}

	rel, err := cleanArtifactOutputPath(raw)
	if err != nil {
		return "", err
	}

	root, err := filepath.Abs(artifactDir)
	if err != nil {
		return "", fmt.Errorf("resolve artifact directory: %w", err)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create artifact directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve artifact directory: %w", err)
	}
	root = filepath.Clean(root)

	target := filepath.Join(root, filepath.FromSlash(rel))
	if !pathInsideOrSame(root, target) {
		return "", fmt.Errorf("artifact path %q escapes artifact directory", rel)
	}
	parent := filepath.Dir(target)
	if err := ensureArtifactOutputParent(root, parent); err != nil {
		return "", err
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return "", fmt.Errorf("create artifact parent directories: %w", err)
	}
	if err := ensureArtifactOutputParent(root, parent); err != nil {
		return "", err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("artifact path %q must not be a symlink", rel)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return target, nil
}

func ensureArtifactOutputParent(root, parent string) error {
	existing, err := nearestExistingArtifactOutputPath(parent)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	if !pathInsideOrSame(root, resolved) {
		return fmt.Errorf("artifact parent path %q escapes artifact directory", parent)
	}
	return nil
}

func nearestExistingArtifactOutputPath(target string) (string, error) {
	current := filepath.Clean(target)
	for {
		if _, err := os.Lstat(current); err == nil {
			return current, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(current)
		if next == current {
			return "", fmt.Errorf("no existing parent for artifact path %q", target)
		}
		current = next
	}
}

func cleanArtifactOutputPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("artifact path must not be empty")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(normalized, "/") ||
		strings.HasPrefix(normalized, "~/") ||
		normalized == "~" ||
		filepath.IsAbs(raw) ||
		hasWindowsDrive(raw) ||
		hasWindowsDrive(normalized) {
		return "", fmt.Errorf("artifact path must be relative")
	}
	if slices.Contains(strings.Split(normalized, "/"), "..") {
		return "", fmt.Errorf("artifact path must not contain parent directory segments")
	}

	clean := path.Clean(normalized)
	if clean == "." {
		return "", fmt.Errorf("artifact path must name a file")
	}
	if strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("artifact path must be relative")
	}
	return clean, nil
}

func hasWindowsDrive(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	ch := value[0]
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z')
}

func pathInsideOrSame(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
