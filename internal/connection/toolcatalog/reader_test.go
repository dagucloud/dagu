// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package toolcatalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/toolcatalog"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

const (
	gmailMCP      = "https://gmailmcp.googleapis.com/mcp/v1"
	calendarMCP   = "https://calendarmcp.googleapis.com/mcp/v1"
	testTimeout   = 30 * time.Second
	catalogMaxAge = 10 * time.Minute
)

// Readers in several processes that find a catalog missing together cause
// one listing, and each receives its result.
func TestCatalogListsOnce(t *testing.T) {
	t.Parallel()
	const processes, readersPerProcess = 4, 25
	const readers = processes * readersPerProcess
	f := newFixture(t)
	// The listing waits until every reader has found the catalog missing,
	// and the holder has read it once more under the lock.
	reads := newReadCounter(readers + 1)
	lister := &fakeLister{wait: func(ctx context.Context) {
		select {
		case <-reads.reached:
		case <-ctx.Done():
		}
	}}

	results := make(chan *connection.Catalog, readers)
	errs := make(chan error, readers)
	var wg sync.WaitGroup
	for range processes {
		r := f.reader(t, lister, func(cfg *toolcatalog.Config) {
			cfg.Store = countingStore{Store: cfg.Store, reads: reads}
		})
		for range readersPerProcess {
			wg.Go(func() {
				catalog, err := r.Catalog(testCtx(t), f.conn, "gmail")
				results <- catalog
				errs <- err
			})
		}
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	for catalog := range results {
		require.Len(t, catalog.Tools, 1)
		assert.Equal(t, "search_threads", catalog.Tools[0].Name)
		assert.NotEmpty(t, catalog.Tools[0].Hash)
	}
	assert.Equal(t, []string{gmailMCP}, lister.endpoints())
}

// A catalog serves readers for 10 minutes; the first reader after that
// lists the service again.
func TestCatalogFreshness(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lister := &fakeLister{}
	r := f.reader(t, lister)
	listedAt := f.clock.now()

	first, err := r.Catalog(testCtx(t), f.conn, "gmail")
	require.NoError(t, err)
	assert.Equal(t, listedAt, first.ListedAt.UTC())

	f.clock.advance(catalogMaxAge - time.Second)
	cached, err := r.Catalog(testCtx(t), f.conn, "gmail")
	require.NoError(t, err)
	assert.True(t, first.ListedAt.Equal(cached.ListedAt))
	assert.Len(t, lister.endpoints(), 1)

	f.clock.advance(time.Second)
	listed, err := r.Catalog(testCtx(t), f.conn, "gmail")
	require.NoError(t, err)
	assert.Equal(t, listedAt.Add(catalogMaxAge), listed.ListedAt.UTC())
	assert.Len(t, lister.endpoints(), 2)
}

// A failed listing returns the server's error and keeps the stored
// catalog, which a later reader lists again.
func TestCatalogListingFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	previous := f.save(t, "gmail", f.clock.now().Add(-catalogMaxAge))
	errQuota := errors.New("quota exceeded")
	lister := &fakeLister{err: errQuota}
	r := f.reader(t, lister)

	_, err := r.Catalog(testCtx(t), f.conn, "gmail")
	require.ErrorIs(t, err, errQuota)
	kept, err := f.store.Catalog(testCtx(t), f.conn, "gmail")
	require.NoError(t, err)
	assert.True(t, previous.ListedAt.Equal(kept.ListedAt))
	assert.Equal(t, previous.Tools, kept.Tools)

	_, err = r.Catalog(testCtx(t), f.conn, "gmail")
	require.ErrorIs(t, err, errQuota)
	assert.Len(t, lister.endpoints(), 2)
}

// Each MCP service of a connection has a catalog of its own. A service the
// connection does not use, or one without an MCP server, lists nothing.
func TestCatalogServices(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lister := &fakeLister{}
	r := f.reader(t, lister)

	for _, service := range []string{"gmail", "calendar"} {
		catalog, err := r.Catalog(testCtx(t), f.conn, service)
		require.NoError(t, err)
		assert.Equal(t, service, catalog.Service)
	}
	assert.Equal(t, []string{gmailMCP, calendarMCP}, lister.endpoints())

	for _, service := range []string{"drive", "mailbox", ""} {
		_, err := r.Catalog(testCtx(t), f.conn, service)
		assert.Error(t, err, service)
	}
	assert.Len(t, lister.endpoints(), 2)
}

// A connection removed during a listing keeps no catalog.
func TestCatalogRemovedConnection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lister := &fakeLister{wait: func(ctx context.Context) {
		assert.NoError(t, f.store.Delete(ctx, f.conn.Key()))
	}}
	r := f.reader(t, lister)

	_, err := r.Catalog(testCtx(t), f.conn, "gmail")
	require.ErrorIs(t, err, connection.ErrNotFound)
}

type fixture struct {
	cfg   *config.Config
	store connection.Store
	conn  *connection.Connection
	clock *clock
}

// newFixture returns a Google connection that uses gmail, calendar, and
// mailbox, without catalogs.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	f := &fixture{
		cfg:   &config.Config{Paths: config.PathsConfig{DataDir: t.TempDir()}},
		clock: &clock{t: time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)},
	}
	s, err := file.NewServerStores(f.cfg).ConnectionStore()
	require.NoError(t, err)
	f.store = s
	app, err := s.CreateApp(ctx, &connection.App{
		Name:         "Workspace",
		Provider:     connection.ProviderGoogle,
		Kind:         connection.AppKindBYO,
		ClientID:     "client",
		ClientSecret: "secret",
		ClientType:   connection.ClientTypeWeb,
		Agent:        true,
	})
	require.NoError(t, err)
	f.conn, err = s.Create(ctx, &connection.Connection{
		Workspace: "ops",
		Name:      "work",
		Provider:  connection.ProviderGoogle,
		App:       app.ID,
		Grant:     connection.GrantAuthorizationCode,
		Services: []connection.Service{
			{Name: "gmail", Access: connection.AccessRead},
			{Name: "calendar", Access: connection.AccessRead},
			{Name: "mailbox", Access: connection.AccessWrite},
		},
	})
	require.NoError(t, err)
	return f
}

// reader returns a reader of a process of its own, which shares the data
// directory with the fixture.
func (f *fixture) reader(t *testing.T, lister toolcatalog.Lister, opts ...func(*toolcatalog.Config)) *toolcatalog.Reader {
	t.Helper()
	stores := file.NewServerStores(f.cfg)
	s, err := stores.ConnectionStore()
	require.NoError(t, err)
	cfg := toolcatalog.Config{
		Store:  s,
		Locks:  stores.ConnectionCatalogLocks(),
		Lister: lister,
		Now:    f.clock.now,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	r, err := toolcatalog.New(cfg)
	require.NoError(t, err)
	return r
}

func (f *fixture) save(t *testing.T, service string, listedAt time.Time) *connection.Catalog {
	t.Helper()
	catalog, err := f.store.SaveCatalog(testCtx(t), f.conn, &connection.Catalog{
		Service:  service,
		ListedAt: listedAt,
		Tools:    []connection.Tool{{Name: "old_tool", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.NoError(t, err)
	return catalog
}

// fakeLister lists one tool, or fails with err, after wait returns.
type fakeLister struct {
	wait func(context.Context)
	err  error

	mu    sync.Mutex
	calls []string
}

func (l *fakeLister) ListTools(ctx context.Context, _ *connection.Connection, endpoint string) ([]connection.Tool, error) {
	l.mu.Lock()
	l.calls = append(l.calls, endpoint)
	l.mu.Unlock()
	if l.wait != nil {
		l.wait(ctx)
	}
	if l.err != nil {
		return nil, l.err
	}
	return []connection.Tool{{
		Name:        "search_threads",
		Description: "Search threads.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Annotations: json.RawMessage(`{"readOnlyHint":true}`),
	}}, nil
}

func (l *fakeLister) endpoints() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

// countingStore counts the catalog reads of every process that shares
// reads.
type countingStore struct {
	connection.Store
	reads *readCounter
}

func (s countingStore) Catalog(ctx context.Context, conn *connection.Connection, service string) (*connection.Catalog, error) {
	defer s.reads.add()
	return s.Store.Catalog(ctx, conn, service)
}

// readCounter closes reached once it counts want reads.
type readCounter struct {
	want    int32
	count   atomic.Int32
	reached chan struct{}
}

func newReadCounter(want int32) *readCounter {
	return &readCounter{want: want, reached: make(chan struct{})}
}

func (c *readCounter) add() {
	if c.count.Add(1) == c.want {
		close(c.reached)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
