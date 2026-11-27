// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
)

var _ agent.ConnectionChecker = (*storeConnectionChecker)(nil)

// storeConnectionChecker checks a run's connections against this server's
// connection store. It opens the store on first use, so a run whose DAG
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
