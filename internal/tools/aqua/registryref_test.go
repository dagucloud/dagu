// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testLatestSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func newLatestRefServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		switch r.URL.Path {
		case "/repos/aquaproj/aqua-registry/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v4.999.0"}`))
		case "/repos/aquaproj/aqua-registry/commits/v4.999.0":
			assert.Equal(t, "application/vnd.github.sha", r.Header.Get("Accept"))
			_, _ = w.Write([]byte(testLatestSHA))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newFailingRefServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestResolveStandardRegistryRefResolvesAndCaches(t *testing.T) {
	t.Parallel()

	calls := 0
	server := newLatestRefServer(t, &calls)
	base := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	installer := New()
	installer.githubAPIBase = server.URL
	installer.now = func() time.Time { return base }
	opts := tools.InstallOptions{ToolsDir: t.TempDir()}

	resolved := installer.resolveStandardRegistryRef(context.Background(), opts, false)
	assert.Equal(t, registryRefSourceLive, resolved.Source)
	assert.Equal(t, "v4.999.0", resolved.Tag)
	assert.Equal(t, testLatestSHA, resolved.SHA)
	callsAfterFirst := calls
	require.Positive(t, callsAfterFirst)

	cached := installer.resolveStandardRegistryRef(context.Background(), opts, false)
	assert.Equal(t, registryRefSourceCache, cached.Source)
	assert.Equal(t, resolved.SHA, cached.SHA)
	assert.Equal(t, callsAfterFirst, calls)

	installer.now = func() time.Time { return base.Add(latestRefCacheTTL + time.Hour) }
	expired := installer.resolveStandardRegistryRef(context.Background(), opts, false)
	assert.Equal(t, registryRefSourceLive, expired.Source)
	assert.Greater(t, calls, callsAfterFirst)
}

func TestResolveStandardRegistryRefForceRefreshSkipsFreshCache(t *testing.T) {
	t.Parallel()

	calls := 0
	server := newLatestRefServer(t, &calls)

	installer := New()
	installer.githubAPIBase = server.URL
	opts := tools.InstallOptions{ToolsDir: t.TempDir()}

	first := installer.resolveStandardRegistryRef(context.Background(), opts, false)
	require.Equal(t, registryRefSourceLive, first.Source)
	callsAfterFirst := calls

	refreshed := installer.resolveStandardRegistryRef(context.Background(), opts, true)
	assert.Equal(t, registryRefSourceLive, refreshed.Source)
	assert.Greater(t, calls, callsAfterFirst)
}

func TestResolveStandardRegistryRefFallsBackToStaleCache(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	opts := tools.InstallOptions{ToolsDir: t.TempDir()}

	stale := New()
	stale.now = func() time.Time { return base }
	stale.writeLatestRefCache(stale.latestRefCachePath(opts), latestRegistryRef{
		Tag:       "v4.900.0",
		SHA:       testLatestSHA,
		FetchedAt: base.Add(-48 * time.Hour),
	})

	server := newFailingRefServer(t)
	installer := New()
	installer.githubAPIBase = server.URL
	installer.now = func() time.Time { return base }

	resolved := installer.resolveStandardRegistryRef(context.Background(), opts, false)
	assert.Equal(t, registryRefSourceStaleCache, resolved.Source)
	assert.Equal(t, "v4.900.0", resolved.Tag)
	assert.Equal(t, testLatestSHA, resolved.SHA)
}

func TestResolveStandardRegistryRefFallsBackToBootstrap(t *testing.T) {
	t.Parallel()

	server := newFailingRefServer(t)
	installer := New()
	installer.githubAPIBase = server.URL

	resolved := installer.resolveStandardRegistryRef(context.Background(), tools.InstallOptions{ToolsDir: t.TempDir()}, false)
	assert.Equal(t, registryRefSourceBootstrap, resolved.Source)
	assert.Equal(t, core.DefaultAquaStandardRegistryRef, resolved.SHA)
	assert.Empty(t, resolved.Tag)
}

func seedFreshRefCache(t *testing.T, installer *Installer, opts tools.InstallOptions) {
	t.Helper()
	installer.writeLatestRefCache(installer.latestRefCachePath(opts), latestRegistryRef{
		Tag:       "v4.999.0",
		SHA:       testLatestSHA,
		FetchedAt: installer.now(),
	})
}

func TestInstallDoesNotRefreshRegistryOnLocalFailure(t *testing.T) {
	t.Parallel()

	calls := 0
	server := newLatestRefServer(t, &calls)

	// A file where the env tree lives makes the install fail locally before
	// any registry or package work starts, while the ref cache stays writable.
	toolsDir := filepath.Join(t.TempDir(), "tools")
	require.NoError(t, os.MkdirAll(filepath.Join(toolsDir, "aqua"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(toolsDir, "aqua", "envs"), []byte("not a dir"), 0o600))

	installer := New()
	installer.githubAPIBase = server.URL
	opts := tools.InstallOptions{ToolsDir: toolsDir}
	seedFreshRefCache(t, installer, opts)
	callsAfterSeed := calls

	_, err := installer.Install(context.Background(), &core.ToolConfig{
		Packages: []core.ToolPackage{{Package: "jqlang/jq", Version: "jq-1.7.1"}},
	}, opts)

	require.Error(t, err)
	assert.False(t, isRegistryResolutionError(err))
	assert.Equal(t, callsAfterSeed, calls, "a local failure must not trigger a registry refresh")
}

func TestInstallDoesNotRefreshRegistryOnCanceledContext(t *testing.T) {
	t.Parallel()

	calls := 0
	server := newLatestRefServer(t, &calls)

	installer := New()
	installer.githubAPIBase = server.URL
	opts := tools.InstallOptions{ToolsDir: filepath.Join(t.TempDir(), "tools")}
	seedFreshRefCache(t, installer, opts)
	callsAfterSeed := calls

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := installer.Install(ctx, &core.ToolConfig{
		Packages: []core.ToolPackage{{Package: "jqlang/jq", Version: "jq-1.7.1"}},
	}, opts)

	require.Error(t, err)
	assert.Equal(t, callsAfterSeed, calls, "a canceled context must not trigger a registry refresh")
}

func TestReadLatestRefCacheRejectsStaleAndInvalid(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	fresh := latestRegistryRef{Tag: "v4.999.0", SHA: testLatestSHA, FetchedAt: now.Add(-time.Hour)}
	stale := latestRegistryRef{Tag: "v4.999.0", SHA: testLatestSHA, FetchedAt: now.Add(-latestRefCacheTTL - time.Hour)}
	future := latestRegistryRef{Tag: "v4.999.0", SHA: testLatestSHA, FetchedAt: now.Add(time.Hour)}
	badSHA := latestRegistryRef{Tag: "v4.999.0", SHA: "not-a-sha", FetchedAt: now.Add(-time.Hour)}

	installer := New()
	writeCase := func(t *testing.T, ref latestRegistryRef) string {
		t.Helper()
		path := t.TempDir() + "/cache.json"
		installer.writeLatestRefCache(path, ref)
		return path
	}

	if cached, ok := readLatestRefCache(writeCase(t, fresh), now); assert.True(t, ok) {
		assert.Equal(t, fresh.SHA, cached.SHA)
	}
	_, ok := readLatestRefCache(writeCase(t, stale), now)
	assert.False(t, ok)
	if cached, ok := readLatestRefCacheAnyAge(writeCase(t, stale)); assert.True(t, ok) {
		assert.Equal(t, stale.SHA, cached.SHA)
	}
	_, ok = readLatestRefCache(writeCase(t, future), now)
	assert.False(t, ok)
	_, ok = readLatestRefCache(writeCase(t, badSHA), now)
	assert.False(t, ok)
	_, ok = readLatestRefCacheAnyAge(writeCase(t, badSHA))
	assert.False(t, ok)
	_, ok = readLatestRefCache("", now)
	assert.False(t, ok)
}

func TestIsCommitSHA(t *testing.T) {
	t.Parallel()

	assert.True(t, isCommitSHA(testLatestSHA))
	assert.True(t, isCommitSHA("080d723b75cd0ea7c2b2059bf6266d3ab39aa792"))
	assert.False(t, isCommitSHA("080D723B75CD0EA7C2B2059BF6266D3AB39AA792"))
	assert.False(t, isCommitSHA("v4.999.0"))
	assert.False(t, isCommitSHA(""))
	assert.False(t, isCommitSHA(testLatestSHA+"aa"))
}
