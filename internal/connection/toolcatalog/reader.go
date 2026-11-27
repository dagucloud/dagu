// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package toolcatalog reads the tool catalogs of connections, listing an
// MCP service again when its catalog is stale. One listing of a catalog
// runs at a time on a host, however many readers find it stale:
//
//	Catalog -> stored catalog fresh? -- yes --> return it
//	             |  no: one listing per catalog in this process
//	             v
//	           TryLock -- held by another --> wait for release,
//	             |                            read the catalog again
//	             v
//	           read again, tools/list, save, unlock
package toolcatalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/connection"
)

const (
	// maxAge is how long a catalog serves readers before a reader lists the
	// service again.
	maxAge = 10 * time.Minute
	// listTimeout bounds one listing, which readers that join it share, so
	// it does not end with the reader that started it.
	listTimeout = 2 * time.Minute
	// mcpService names the catalog of an mcp connection, whose one server
	// is no preset service.
	mcpService = "mcp"
)

// Lister lists the tools of an MCP server for a connection.
type Lister interface {
	// ListTools returns every tool that the MCP server at endpoint lists,
	// sending the connection's token. It returns the server's error when
	// the listing fails.
	ListTools(ctx context.Context, conn *connection.Connection, endpoint string) ([]connection.Tool, error)
}

// Config is what a Reader needs.
type Config struct {
	Store  connection.Store
	Locks  connection.CatalogLocks
	Lister Lister
	// Now returns the current time. It defaults to time.Now.
	Now func() time.Time
}

// Reader returns the tool catalogs of connections.
type Reader struct {
	store  connection.Store
	locks  connection.CatalogLocks
	lister Lister
	now    func() time.Time

	mu sync.Mutex
	// listings holds the listings in flight, so it never holds more entries
	// than there are catalogs being listed.
	listings map[listingKey]*listing
}

type listingKey struct {
	connectionID string
	service      string
}

// listing is one listing of a catalog, whose result every reader that
// joins it receives.
type listing struct {
	done    chan struct{}
	catalog *connection.Catalog
	err     error
}

// target is the catalog a reader asks for and the MCP server it lists.
type target struct {
	service  string
	endpoint string
}

// New returns a reader over cfg's store, locks, and lister.
func New(cfg Config) (*Reader, error) {
	if cfg.Store == nil || cfg.Locks == nil || cfg.Lister == nil {
		return nil, errors.New("tool catalog: store, locks, and lister are required")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Reader{
		store:    cfg.Store,
		locks:    cfg.Locks,
		lister:   cfg.Lister,
		now:      now,
		listings: make(map[listingKey]*listing),
	}, nil
}

// Catalog returns the catalog of service for conn. A catalog listed less
// than 10 minutes ago is returned as stored. An older or missing one is
// listed again, once on a host however many readers ask for it together;
// a failed listing returns the server's error and keeps the stored
// catalog. service names one of conn's preset services that has an MCP
// server, or is empty for an mcp connection.
func (r *Reader) Catalog(ctx context.Context, conn *connection.Connection, service string) (*connection.Catalog, error) {
	if conn == nil {
		return nil, connection.ErrNotFound
	}
	t, err := resolve(conn, service)
	if err != nil {
		return nil, err
	}
	if catalog, ok, err := r.fresh(ctx, conn, t.service); ok || err != nil {
		return catalog, err
	}
	l := r.join(ctx, conn, t)
	select {
	case <-l.done:
		return l.catalog, l.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// join returns the listing of t in flight, starting one when there is
// none.
func (r *Reader) join(ctx context.Context, conn *connection.Connection, t target) *listing {
	key := listingKey{connectionID: conn.ID, service: t.service}
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.listings[key]; ok {
		return l
	}
	l := &listing{done: make(chan struct{})}
	r.listings[key] = l
	go func() {
		listCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), listTimeout)
		defer cancel()
		l.catalog, l.err = r.list(listCtx, conn, t)
		r.mu.Lock()
		delete(r.listings, key)
		r.mu.Unlock()
		close(l.done)
	}()
	return l
}

// list lists t under its catalog lock. A reader that finds the lock held
// waits for the holder and reads its result; when the holder left no
// fresh catalog, as after a failed listing, the reader lists itself.
func (r *Reader) list(ctx context.Context, conn *connection.Connection, t target) (*connection.Catalog, error) {
	for {
		lock, err := r.locks.TryLock(ctx, conn.Key(), t.service)
		if errors.Is(err, connection.ErrLocked) {
			if err := r.locks.WaitUnlocked(ctx, conn.Key(), t.service); err != nil {
				return nil, err
			}
			if catalog, ok, err := r.fresh(ctx, conn, t.service); ok || err != nil {
				return catalog, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		return r.listLocked(ctx, conn, t, lock)
	}
}

// listLocked lists t while lock is held and releases it.
func (r *Reader) listLocked(ctx context.Context, conn *connection.Connection, t target, lock connection.Lock) (*connection.Catalog, error) {
	defer lock.Unlock()
	// Another process may have listed between the first read and the lock.
	if catalog, ok, err := r.fresh(ctx, conn, t.service); ok || err != nil {
		return catalog, err
	}
	tools, err := r.lister.ListTools(ctx, conn, t.endpoint)
	if err != nil {
		return nil, fmt.Errorf("connection %q: list the tools of %s: %w", conn.Name, t.endpoint, err)
	}
	if err := lock.Held(); err != nil {
		return nil, err
	}
	return r.store.SaveCatalog(ctx, conn, &connection.Catalog{
		Service:  t.service,
		ListedAt: r.now(),
		Tools:    tools,
	})
}

// fresh returns the stored catalog of service when it serves readers.
func (r *Reader) fresh(ctx context.Context, conn *connection.Connection, service string) (*connection.Catalog, bool, error) {
	catalog, err := r.store.Catalog(ctx, conn, service)
	if errors.Is(err, connection.ErrCatalogNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return catalog, r.now().Before(catalog.ListedAt.Add(maxAge)), nil
}

// resolve returns the catalog and MCP server of service for conn.
func resolve(conn *connection.Connection, service string) (target, error) {
	if conn.Provider == connection.ProviderMCP {
		if service != "" {
			return target{}, fmt.Errorf("connection %q: an mcp connection has no service %q", conn.Name, service)
		}
		if conn.Resource == "" {
			return target{}, fmt.Errorf("connection %q has no MCP server URL", conn.Name)
		}
		return target{service: mcpService, endpoint: conn.Resource}, nil
	}
	if !slices.ContainsFunc(conn.Services, func(s connection.Service) bool { return s.Name == service }) {
		return target{}, fmt.Errorf("connection %q does not use service %q", conn.Name, service)
	}
	preset, ok := connection.LookupPreset(conn.Provider, service)
	if !ok || preset.MCPURL == "" {
		return target{}, fmt.Errorf("connection %q: service %q has no MCP server", conn.Name, service)
	}
	return target{service: service, endpoint: preset.MCPURL}, nil
}
