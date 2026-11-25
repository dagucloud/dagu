// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"net"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/spf13/cobra"
)

func RestoreDAGFromStatusForTest(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus) (*ir.DAG, error) {
	return restoreDAGFromStatus(ctx, dag, status, nil)
}

var WorkerFlagsForTest = workerFlags

// EgressProxyForTest returns the egress-proxy command on the given listen and
// dial functions.
func EgressProxyForTest(listen func(network, address string) (net.Listener, error), dial egress.DialFunc) *cobra.Command {
	return egressProxy(listen, dial)
}
