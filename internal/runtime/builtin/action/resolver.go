// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
)

const (
	actionPrefixSource = "source:"
	actionPrefixPkg    = "pkg:"

	bundleModeSource  = "source"
	bundleModePackage = "package"
)

type resolveOptions struct {
	ToolsDir    string
	CacheDir    string
	RegistryDir string
	WorkDir     string
}

type actionBundle struct {
	Mode        string
	RootDir     string
	OriginalRef string
	ResolvedRef string
	Version     string
}

func resolveBundle(ctx context.Context, ref string, opts resolveOptions) (*actionBundle, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, actionPrefixSource):
		return resolveSourceBundle(ctx, ref, opts)
	case strings.HasPrefix(ref, actionPrefixPkg):
		return resolvePackageBundle(ref, opts)
	default:
		return nil, fmt.Errorf("action ref must start with %q or %q", actionPrefixSource, actionPrefixPkg)
	}
}

func resolveSourceBundle(ctx context.Context, ref string, opts resolveOptions) (*actionBundle, error) {
	target, version, err := splitVersionedRef(strings.TrimPrefix(ref, actionPrefixSource))
	if err != nil {
		return nil, err
	}
	if dir, ok := localSourceDir(target, opts.WorkDir); ok {
		return &actionBundle{
			Mode:        bundleModeSource,
			RootDir:     dir,
			OriginalRef: ref,
			ResolvedRef: dir,
			Version:     version,
		}, nil
	}
	root, resolved, err := cloneGitSource(ctx, target, version, opts)
	if err != nil {
		return nil, err
	}
	return &actionBundle{
		Mode:        bundleModeSource,
		RootDir:     root,
		OriginalRef: ref,
		ResolvedRef: resolved,
		Version:     version,
	}, nil
}

func resolvePackageBundle(ref string, opts resolveOptions) (*actionBundle, error) {
	target, version, err := splitVersionedRef(strings.TrimPrefix(ref, actionPrefixPkg))
	if err != nil {
		return nil, err
	}
	if dir, ok := localPackageDir(target); ok {
		return &actionBundle{
			Mode:        bundleModePackage,
			RootDir:     dir,
			OriginalRef: ref,
			ResolvedRef: dir,
			Version:     version,
		}, nil
	}
	if err := validatePackageName(target); err != nil {
		return nil, err
	}
	if err := validatePackageVersion(version); err != nil {
		return nil, err
	}
	registryDir := packageRegistryDir(opts)
	if registryDir == "" {
		return nil, fmt.Errorf("package action registry dir is required")
	}
	root := filepath.Join(registryDir, filepath.FromSlash(target), version)
	if !isPathWithin(registryDir, root) {
		return nil, fmt.Errorf("invalid package action path")
	}
	if info, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("stat package action: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("package action path must be a directory")
	}
	return &actionBundle{
		Mode:        bundleModePackage,
		RootDir:     root,
		OriginalRef: ref,
		ResolvedRef: target,
		Version:     version,
	}, nil
}

func splitVersionedRef(ref string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	idx := strings.LastIndex(ref, "@")
	if idx <= 0 || idx == len(ref)-1 {
		return "", "", fmt.Errorf(`action ref must be "target@version"`)
	}
	return strings.TrimSpace(ref[:idx]), strings.TrimSpace(ref[idx+1:]), nil
}

func localSourceDir(target string, workDir string) (string, bool) {
	if dir, ok := fileURLDir(target); ok {
		return dir, true
	}
	candidate := target
	if !filepath.IsAbs(candidate) && strings.TrimSpace(workDir) != "" {
		candidate = filepath.Join(strings.TrimSpace(workDir), candidate)
	}
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return filepath.Clean(candidate), true
		}
		return filepath.Clean(abs), true
	}
	return "", false
}

func localPackageDir(target string) (string, bool) {
	return fileURLDir(target)
}

func fileURLDir(target string) (string, bool) {
	if !strings.HasPrefix(target, "file://") {
		return "", false
	}
	u, err := url.Parse(target)
	if err != nil || u.Path == "" {
		return "", false
	}
	return filepath.Clean(u.Path), true
}

func cloneGitSource(ctx context.Context, target, version string, opts resolveOptions) (string, string, error) {
	cacheDir := actionCacheDir(opts)
	if cacheDir == "" {
		return "", "", fmt.Errorf("action cache dir is required for remote source actions")
	}
	repoURL := gitURL(target)
	key := hashRef(repoURL + "@" + version)
	root := filepath.Join(cacheDir, "source", key)
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
		resolved, err := gitRevParse(ctx, root)
		return root, resolved, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o750); err != nil {
		return "", "", fmt.Errorf("create action source cache: %w", err)
	}
	tmp := fmt.Sprintf("%s.%d.tmp", root, os.Getpid())
	_ = os.RemoveAll(tmp)
	if err := runGit(ctx, "", "clone", "--depth", "1", "--branch", version, repoURL, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		if err := runGit(ctx, "", "clone", repoURL, tmp); err != nil {
			return "", "", fmt.Errorf("clone action source: %w", err)
		}
		if err := runGit(ctx, tmp, "checkout", version); err != nil {
			_ = os.RemoveAll(tmp)
			return "", "", fmt.Errorf("checkout action source ref: %w", err)
		}
	}
	if err := os.Rename(tmp, root); err != nil {
		if info, statErr := os.Stat(filepath.Join(root, ".git")); statErr == nil && info.IsDir() {
			_ = os.RemoveAll(tmp)
			resolved, revErr := gitRevParse(ctx, root)
			return root, resolved, revErr
		}
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("store action source cache: %w", err)
	}
	resolved, err := gitRevParse(ctx, root)
	return root, resolved, err
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
	cmd.Dir = dir
	cmdutil.SetupCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitRevParse(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD") //nolint:gosec
	cmd.Dir = dir
	cmdutil.SetupCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitURL(target string) string {
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "ssh://") || strings.HasPrefix(target, "git@") {
		return target
	}
	if strings.HasPrefix(target, "github.com/") {
		return "https://" + target + ".git"
	}
	return target
}

func actionCacheDir(opts resolveOptions) string {
	if strings.TrimSpace(opts.CacheDir) != "" {
		return strings.TrimSpace(opts.CacheDir)
	}
	if strings.TrimSpace(opts.ToolsDir) == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(opts.ToolsDir), "actions")
}

func packageRegistryDir(opts resolveOptions) string {
	if strings.TrimSpace(opts.RegistryDir) != "" {
		return strings.TrimSpace(opts.RegistryDir)
	}
	if strings.TrimSpace(opts.ToolsDir) == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(opts.ToolsDir), "actions", "registry")
}

func hashRef(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validatePackageName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid package name %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid package name %q", name)
		}
	}
	return nil
}

func validatePackageVersion(version string) error {
	version = strings.TrimSpace(version)
	if version == "" || strings.ContainsAny(version, `/\`) || version == "." || version == ".." {
		return fmt.Errorf("invalid package version %q", version)
	}
	return nil
}

func safeRelativePath(rootDir, relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" || filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative")
	}
	path := filepath.Join(rootDir, relPath)
	if !isPathWithin(rootDir, path) {
		return "", fmt.Errorf("path escapes action directory")
	}
	return filepath.Clean(path), nil
}

func isPathWithin(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
