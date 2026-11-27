// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// ConnectionChecker checks a connection that a run's DAG declares, before
// the run's first step.
type ConnectionChecker interface {
	// CheckConnection returns an error that names the connection when a run
	// of a DAG in workspace cannot use ref. The workspace is empty for a DAG
	// without a workspace label.
	CheckConnection(ctx context.Context, workspace string, ref ir.ConnectionRef) error
}

// NewStoreConnectionChecker returns a checker that admits connections
// against the store open returns. It calls open once, on the first check.
func NewStoreConnectionChecker(open func() (connection.Store, error)) ConnectionChecker {
	return &storeConnectionChecker{open: open}
}

// storeConnectionChecker opens its store on first use, so a run whose DAG
// declares no connection reads no connection file.
type storeConnectionChecker struct {
	open  func() (connection.Store, error)
	once  sync.Once
	store connection.Store
	err   error
}

func (c *storeConnectionChecker) CheckConnection(ctx context.Context, workspace string, ref ir.ConnectionRef) error {
	c.once.Do(func() { c.store, c.err = c.open() })
	if c.err != nil {
		return fmt.Errorf("connection %q: %w", ref.Name, c.err)
	}
	return connection.Admit(ctx, c.store, workspace, ref.Name, ref.Servers)
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
