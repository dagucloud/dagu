// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/tools"
)

const (
	defaultGitHubAPIBase   = "https://api.github.com"
	aquaRegistryRepo       = "aquaproj/aqua-registry"
	latestRefCacheFileName = "latest-standard-registry.json"
	latestRefCacheTTL      = 24 * time.Hour
	latestRefMaxBody       = 1 << 20
)

// latestRegistryRef records a resolved aqua standard registry release.
type latestRegistryRef struct {
	Tag       string    `json:"tag"`
	SHA       string    `json:"sha"`
	FetchedAt time.Time `json:"fetchedAt"`
}

type registryRefSource int

const (
	registryRefSourceCache registryRefSource = iota
	registryRefSourceLive
	registryRefSourceStaleCache
	registryRefSourceBootstrap
)

type resolvedRegistryRef struct {
	latestRegistryRef
	Source registryRefSource
}

// resolveStandardRegistryRef returns the standard registry ref to use when the
// DAG does not pin one: the latest aqua-registry release, served from a fresh
// disk cache when available. When the latest release cannot be resolved, a
// previously cached ref of any age is used, then the compiled-in bootstrap
// ref, so resolution never fails.
func (i *Installer) resolveStandardRegistryRef(ctx context.Context, opts tools.InstallOptions, forceRefresh bool) resolvedRegistryRef {
	cachePath := i.latestRefCachePath(opts)
	if !forceRefresh {
		if cached, ok := readLatestRefCache(cachePath, i.now()); ok {
			return resolvedRegistryRef{cached, registryRefSourceCache}
		}
	}

	ref, err := i.fetchLatestRegistryRef(ctx)
	if err == nil {
		i.writeLatestRefCache(cachePath, ref)
		return resolvedRegistryRef{ref, registryRefSourceLive}
	}

	if cached, ok := readLatestRefCacheAnyAge(cachePath); ok {
		logger.Info(ctx, "Using the cached aqua registry release; latest release resolution failed",
			slog.String("registry", cached.Tag), slog.Any("err", err))
		return resolvedRegistryRef{cached, registryRefSourceStaleCache}
	}

	logger.Info(ctx, "Using the bootstrap aqua registry ref; latest release resolution failed",
		slog.Any("err", err))
	return resolvedRegistryRef{
		latestRegistryRef{SHA: core.DefaultAquaStandardRegistryRef},
		registryRefSourceBootstrap,
	}
}

func (i *Installer) fetchLatestRegistryRef(ctx context.Context) (latestRegistryRef, error) {
	tag, err := i.fetchLatestRegistryTag(ctx)
	if err != nil {
		return latestRegistryRef{}, err
	}
	sha, err := i.fetchRegistryCommitSHA(ctx, tag)
	if err != nil {
		return latestRegistryRef{}, err
	}
	return latestRegistryRef{Tag: tag, SHA: sha, FetchedAt: i.now()}, nil
}

func (i *Installer) latestRefCachePath(opts tools.InstallOptions) string {
	dir := toolsDir(opts)
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	return filepath.Join(dir, providerAqua, latestRefCacheFileName)
}

func readLatestRefCache(path string, now time.Time) (latestRegistryRef, bool) {
	cached, ok := readLatestRefCacheAnyAge(path)
	if !ok {
		return latestRegistryRef{}, false
	}
	if cached.FetchedAt.IsZero() || now.Before(cached.FetchedAt) || now.Sub(cached.FetchedAt) > latestRefCacheTTL {
		return latestRegistryRef{}, false
	}
	return cached, true
}

func readLatestRefCacheAnyAge(path string) (latestRegistryRef, bool) {
	if path == "" {
		return latestRegistryRef{}, false
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return latestRegistryRef{}, false
	}
	var cached latestRegistryRef
	if err := json.Unmarshal(data, &cached); err != nil {
		return latestRegistryRef{}, false
	}
	if cached.Tag == "" || !isCommitSHA(cached.SHA) {
		return latestRegistryRef{}, false
	}
	return cached, true
}

func (i *Installer) writeLatestRefCache(path string, ref latestRegistryRef) {
	if path == "" {
		return
	}
	data, err := json.Marshal(ref)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		i.logger.Debug("write aqua latest registry cache", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		i.logger.Debug("write aqua latest registry cache", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		i.logger.Debug("write aqua latest registry cache", "err", err)
	}
}

func (i *Installer) fetchLatestRegistryTag(ctx context.Context) (string, error) {
	body, err := i.githubGet(ctx, "/repos/"+aquaRegistryRepo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("parse latest aqua-registry release: %w", err)
	}
	tag := strings.TrimSpace(release.TagName)
	if tag == "" {
		return "", fmt.Errorf("latest aqua-registry release has no tag")
	}
	return tag, nil
}

func (i *Installer) fetchRegistryCommitSHA(ctx context.Context, tag string) (string, error) {
	body, err := i.githubGet(ctx, "/repos/"+aquaRegistryRepo+"/commits/"+tag, "application/vnd.github.sha")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(body))
	if !isCommitSHA(sha) {
		return "", fmt.Errorf("resolve aqua-registry commit for %q: unexpected response", tag)
	}
	return sha, nil
}

func (i *Installer) githubGet(ctx context.Context, path, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, i.githubAPIBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "dagu")
	if token := githubToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, latestRefMaxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return body, nil
}

func githubToken() string {
	for _, key := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	return ""
}

func isCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
