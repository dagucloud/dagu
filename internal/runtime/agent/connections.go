// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// errNoConnectionTokens fails a token request in a process that has the
// connection store but no locks to refresh under.
var errNoConnectionTokens = errors.New("connection tokens are not available in this process")

// ConnectionChecker checks a connection that a run's DAG declares, before
// the run's first step, and hands the declared connections to the run.
type ConnectionChecker interface {
	// CheckConnection returns an error that names the connection when a run
	// of a DAG in workspace cannot use ref. The workspace is empty for a DAG
	// without a workspace label.
	CheckConnection(ctx context.Context, workspace string, ref ir.ConnectionRef) error
	// Connections returns where a run of a DAG in workspace gets the
	// connections it declares and their tokens.
	Connections(workspace string) runtime.Connections
}

// NewStoreConnectionChecker returns a checker that admits connections
// against the store open returns. Its runs take tokens from one broker
// over that store and locks, which keeps the tokens it hands out, so a
// run and its in-process sub-DAGs share them. It calls open once, on
// first use.
func NewStoreConnectionChecker(open func() (connection.Store, error), locks connection.Locks) ConnectionChecker {
	return &storeConnectionChecker{open: open, locks: locks}
}

// storeConnectionChecker opens its store on first use, so a run whose DAG
// declares no connection reads no connection file.
type storeConnectionChecker struct {
	open   func() (connection.Store, error)
	locks  connection.Locks
	once   sync.Once
	store  connection.Store
	tokens *broker.Broker
	err    error
}

func (c *storeConnectionChecker) load() error {
	c.once.Do(func() {
		if c.store, c.err = c.open(); c.err != nil || c.locks == nil {
			return
		}
		c.tokens, c.err = broker.New(broker.Config{Store: c.store, Locks: c.locks, CacheTokens: true})
	})
	return c.err
}

func (c *storeConnectionChecker) CheckConnection(ctx context.Context, workspace string, ref ir.ConnectionRef) error {
	if err := c.load(); err != nil {
		return fmt.Errorf("connection %q: %w", ref.Name, err)
	}
	return connection.Admit(ctx, c.store, workspace, ref.Name, ref.Servers)
}

func (c *storeConnectionChecker) Connections(workspace string) runtime.Connections {
	return &storeConnections{
		checker:   c,
		workspace: secret.NormalizeWorkspace(workspace),
		conns:     make(map[string]*connection.Connection),
	}
}

// storeConnections are the connections of one workspace. It keeps each
// connection it reads, so it reads each record once; a run names only the
// connections its DAG declares.
type storeConnections struct {
	checker   *storeConnectionChecker
	workspace string

	mu    sync.Mutex
	conns map[string]*connection.Connection
}

var _ runtime.Connections = (*storeConnections)(nil)

func (s *storeConnections) Connection(ctx context.Context, name string) (*connection.Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conn, ok := s.conns[name]; ok {
		return conn, nil
	}
	if err := s.checker.load(); err != nil {
		return nil, fmt.Errorf("connection %q: %w", name, err)
	}
	conn, err := s.checker.store.Get(ctx, connection.Key{Workspace: s.workspace, Name: name})
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", name, err)
	}
	s.conns[name] = conn
	return conn, nil
}

func (s *storeConnections) Token(ctx context.Context, conn *connection.Connection, resource string, minTTL time.Duration) (string, time.Duration, error) {
	tokens, err := s.tokens()
	if err != nil {
		return "", 0, err
	}
	return tokens.Token(ctx, conn, resource, minTTL)
}

func (s *storeConnections) Rejected(ctx context.Context, conn *connection.Connection, resource, token string, minTTL time.Duration) (string, time.Duration, error) {
	tokens, err := s.tokens()
	if err != nil {
		return "", 0, err
	}
	return tokens.Rejected(ctx, conn, resource, token, minTTL)
}

func (s *storeConnections) tokens() (*broker.Broker, error) {
	if err := s.checker.load(); err != nil {
		return nil, err
	}
	if s.checker.tokens == nil {
		return nil, errNoConnectionTokens
	}
	return s.checker.tokens, nil
}

// runConnections returns where the run gets its connections, or nil when
// the run checks none or is a workflow test case, which reads no
// connection record.
func (a *Agent) runConnections() runtime.Connections {
	if a.connectionChecker == nil || a.testSideEffects != nil || len(a.dag.Connections) == 0 {
		return nil
	}
	name, state := workspace.WorkspaceLabelFromLabels(a.dag.Labels)
	if state == workspace.WorkspaceLabelInvalid {
		return nil
	}
	return a.connectionChecker.Connections(name)
}

// checkConnections checks every connection the DAG declares and returns
// the failures together. A workflow test case reads no connection record,
// as it reads no secret through a provider.
func (a *Agent) checkConnections(ctx context.Context) error {
	if a.connectionChecker == nil || a.testSideEffects != nil {
		return nil
	}
	name, state := workspace.WorkspaceLabelFromLabels(a.dag.Labels)
	var errs []error
	for _, ref := range a.dag.Connections {
		// A malformed or ambiguous label names no workspace, and only a DAG
		// without the label may use the .global connections.
		if state == workspace.WorkspaceLabelInvalid {
			errs = append(errs, fmt.Errorf("connection %q: the DAG's workspace labels name no single valid workspace", ref.Name))
			continue
		}
		if err := a.connectionChecker.CheckConnection(ctx, name, ref); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
