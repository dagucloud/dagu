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
	cmnconfig "github.com/dagucloud/dagu/internal/cmn/config"
	dagutools "github.com/dagucloud/dagu/internal/tools"
)

const (
	actionPrefixSource = "source:"
	envToolsDir        = "DAGU_TOOLS_DIR"

	officialActionOwner      = "dagucloud"
	officialActionRepoPrefix = "action-"
)

var (
	githubOwnerRegexp = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	githubRepoRegexp  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	fullGitSHARegexp  = regexp.MustCompile(`^[A-Fa-f0-9]{40}$`)
)

type resolveOptions struct {
	ToolsDir string
	CacheDir string
	WorkDir  string
}

type actionBundle struct {
	RootDir     string
	ResolvedRef string
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
	return &actionBundle{RootDir: root, ResolvedRef: resolved}, nil
}

func resolveSourceBundle(ctx context.Context, ref string, opts resolveOptions) (*actionBundle, error) {
	target, version, err := splitVersionedRef(strings.TrimPrefix(ref, actionPrefixSource))
	if err != nil {
		return nil, err
	}
	if dir, ok := localSourceDir(target, opts.WorkDir); ok {
		return &actionBundle{RootDir: dir, ResolvedRef: "local"}, nil
	}
	root, resolved, err := cloneGitSource(ctx, target, version, opts)
	if err != nil {
		return nil, err
	}
	return &actionBundle{RootDir: root, ResolvedRef: resolved}, nil
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
	repoKey := hashRef(repoURL)
	resolvedHint, useShallowClone := resolveGitSourceRef(ctx, repoURL, version)
	if resolvedHint != "" {
		root := actionSourceCachePath(cacheDir, repoKey, resolvedHint)
		if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
			resolved, err := gitRevParse(ctx, root)
			return root, resolved, err
		}
	}
	baseDir := filepath.Join(cacheDir, "source", repoKey)
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return "", "", fmt.Errorf("create action source cache: %w", err)
	}
	tmp, err := os.MkdirTemp(baseDir, "checkout-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("create action source temp dir: %w", err)
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmp)
		}
	}()

	if useShallowClone {
		err = runGit(ctx, "", "clone", "--depth", "1", "--branch", version, repoURL, tmp)
	} else {
		err = fmt.Errorf("shallow clone skipped for unresolved ref")
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		if err := runGit(ctx, "", "clone", repoURL, tmp); err != nil {
			return "", "", fmt.Errorf("clone action source: %w", err)
		}
		if err := runGit(ctx, tmp, "checkout", version); err != nil {
			return "", "", fmt.Errorf("checkout action source ref: %w", err)
		}
	}

	resolved, err := gitRevParse(ctx, tmp)
	if err != nil {
		return "", "", err
	}
	root := actionSourceCachePath(cacheDir, repoKey, resolved)
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
		return root, resolved, nil
	}
	if err := os.Rename(tmp, root); err != nil {
		if info, statErr := os.Stat(filepath.Join(root, ".git")); statErr == nil && info.IsDir() {
			resolved, revErr := gitRevParse(ctx, root)
			return root, resolved, revErr
		}
		return "", "", fmt.Errorf("store action source cache: %w", err)
	}
	cleanupTmp = false
	return root, resolved, nil
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

func gitOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec
	cmd.Dir = dir
	cmdutil.SetupCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
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

func resolveGitSourceRef(ctx context.Context, repoURL, version string) (string, bool) {
	if fullGitSHARegexp.MatchString(version) {
		return strings.ToLower(version), false
	}
	out, err := gitOutput(ctx, "", "ls-remote", repoURL,
		"refs/tags/"+version+"^{}",
		"refs/tags/"+version,
		"refs/heads/"+version,
	)
	if err != nil {
		return "", true
	}
	return parseLsRemoteResolvedSHA(string(out), version), true
}

func parseLsRemoteResolvedSHA(output, version string) string {
	var headSHA, tagSHA, peeledTagSHA string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sha, ref := fields[0], fields[1]
		switch ref {
		case "refs/tags/" + version + "^{}":
			peeledTagSHA = sha
		case "refs/tags/" + version:
			tagSHA = sha
		case "refs/heads/" + version:
			headSHA = sha
		}
	}
	switch {
	case peeledTagSHA != "":
		return peeledTagSHA
	case tagSHA != "":
		return tagSHA
	default:
		return headSHA
	}
}

func actionSourceCachePath(cacheDir, repoKey, resolvedSHA string) string {
	return filepath.Join(cacheDir, "source", repoKey, resolvedSHA)
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
	switch len(parts) {
	case 1:
		name := parts[0]
		if !isValidOfficialActionName(name) {
			return "", fmt.Errorf("invalid official action name %q", name)
		}
		return "https://github.com/" + officialActionOwner + "/" + officialActionRepoPrefix + name + ".git", nil
	case 2:
		owner, repo := parts[0], parts[1]
		if !githubOwnerRegexp.MatchString(owner) || strings.HasSuffix(owner, "-") {
			return "", fmt.Errorf("invalid GitHub action owner %q", owner)
		}
		if !githubRepoRegexp.MatchString(repo) || repo == "." || repo == ".." || strings.HasSuffix(repo, ".git") {
			return "", fmt.Errorf("invalid GitHub action repository %q", repo)
		}
		return "https://github.com/" + owner + "/" + repo + ".git", nil
	default:
		return "", fmt.Errorf("GitHub action ref target must be action or owner/repo")
	}
}

func isValidOfficialActionName(name string) bool {
	name = strings.TrimSpace(name)
	return githubRepoRegexp.MatchString(name) &&
		name != "." &&
		name != ".." &&
		!strings.HasPrefix(name, ".") &&
		!strings.HasPrefix(name, "-") &&
		!strings.HasSuffix(name, ".git")
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
