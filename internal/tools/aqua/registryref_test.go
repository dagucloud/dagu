// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestLatestStandardRegistryRefResolvesAndCaches(t *testing.T) {
	t.Parallel()

	calls := 0
	server := newLatestRefServer(t, &calls)
	base := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	installer := New()
	installer.githubAPIBase = server.URL
	installer.now = func() time.Time { return base }
	opts := tools.InstallOptions{ToolsDir: t.TempDir()}

	ref, err := installer.latestStandardRegistryRef(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "v4.999.0", ref.Tag)
	assert.Equal(t, testLatestSHA, ref.SHA)
	callsAfterFirst := calls
	require.Positive(t, callsAfterFirst)

	cached, err := installer.latestStandardRegistryRef(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, ref, cached)
	assert.Equal(t, callsAfterFirst, calls)

	installer.now = func() time.Time { return base.Add(latestRefCacheTTL + time.Hour) }
	_, err = installer.latestStandardRegistryRef(context.Background(), opts)
	require.NoError(t, err)
	assert.Greater(t, calls, callsAfterFirst)
}

func TestLatestStandardRegistryRefWithoutToolsDirSkipsCache(t *testing.T) {
	t.Parallel()

	calls := 0
	server := newLatestRefServer(t, &calls)

	installer := New()
	installer.githubAPIBase = server.URL

	ref, err := installer.latestStandardRegistryRef(context.Background(), tools.InstallOptions{})
	require.NoError(t, err)
	assert.Equal(t, testLatestSHA, ref.SHA)

	_, err = installer.latestStandardRegistryRef(context.Background(), tools.InstallOptions{})
	require.NoError(t, err)
	assert.Equal(t, 4, calls)
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
	_, ok = readLatestRefCache(writeCase(t, future), now)
	assert.False(t, ok)
	_, ok = readLatestRefCache(writeCase(t, badSHA), now)
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
