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
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
)

const (
	actionPrefixSource = "source:"

	bundleModeSource = "source"
)

var (
	githubOwnerRegexp = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubRepoRegexp  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type resolveOptions struct {
	ToolsDir string
	CacheDir string
	WorkDir  string
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
	case strings.HasPrefix(ref, "pkg:"):
		return nil, fmt.Errorf("package action references must use GitHub owner/repo@version")
	default:
		return resolveGitHubBundle(ctx, ref, opts)
	}
}

func resolveGitHubBundle(ctx context.Context, ref string, opts resolveOptions) (*actionBundle, error) {
	target, version, err := splitVersionedRef(ref)
	if err != nil {
		return nil, err
	}
	repoURL, err := githubRepoURL(target)
	if err != nil {
		return nil, err
	}
	root, resolved, err := cloneGitSource(ctx, repoURL, version, opts)
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
	if err := validateGitRef(version); err != nil {
		return "", "", err
	}
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
	tmp, err := os.MkdirTemp(filepath.Dir(root), filepath.Base(root)+".*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("create action source temp dir: %w", err)
	}
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

func githubRepoURL(target string) (string, error) {
	target = strings.TrimSpace(target)
	parts := strings.Split(target, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("GitHub action ref target must be owner/repo")
	}
	owner, repo := parts[0], parts[1]
	if !githubOwnerRegexp.MatchString(owner) || strings.HasSuffix(owner, "-") {
		return "", fmt.Errorf("invalid GitHub action owner %q", owner)
	}
	if !githubRepoRegexp.MatchString(repo) || repo == "." || repo == ".." || strings.HasSuffix(repo, ".git") {
		return "", fmt.Errorf("invalid GitHub action repository %q", repo)
	}
	return "https://github.com/" + owner + "/" + repo + ".git", nil
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

func hashRef(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func safeRelativePath(rootDir, relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" || isAbsoluteActionPath(relPath) {
		return "", fmt.Errorf("path must be relative")
	}
	slashPath := path.Clean(strings.ReplaceAll(relPath, `\`, "/"))
	if slashPath == "." || slashPath == ".." || strings.HasPrefix(slashPath, "../") {
		return "", fmt.Errorf("path escapes action directory")
	}
	resolvedPath := filepath.Join(rootDir, filepath.FromSlash(slashPath))
	if !isPathWithin(rootDir, resolvedPath) {
		return "", fmt.Errorf("path escapes action directory")
	}
	return filepath.Clean(resolvedPath), nil
}

func validateGitRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("action git ref is required")
	}
	if strings.HasPrefix(ref, "-") ||
		strings.ContainsAny(ref, " \t\r\n\\~^:?*[]") ||
		strings.Contains(ref, "..") ||
		strings.Contains(ref, "@{") ||
		strings.Contains(ref, "//") ||
		strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") ||
		strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("invalid action git ref %q", ref)
	}
	for part := range strings.SplitSeq(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("invalid action git ref %q", ref)
		}
	}
	return nil
}

func isPathWithin(dir, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
