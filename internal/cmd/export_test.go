// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"net"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/spf13/cobra"
)

func RestoreDAGFromStatusForTest(ctx context.Context, dag *ir.DAG, status *ir.DAGRunStatus) (*ir.DAG, error) {
	return restoreDAGFromStatus(ctx, dag, status, nil, baseConfigOptions(config.GetConfig(ctx)))
}

var WorkerFlagsForTest = workerFlags

// SetInviteRegistryForTest makes dagu worker invite call the coordinators
// of registry until the test ends.
func SetInviteRegistryForTest(t *testing.T, registry serviceregistry.ServiceRegistry) {
	t.Helper()
	previous := inviteRegistry
	inviteRegistry = func(*Context) (serviceregistry.ServiceRegistry, error) { return registry, nil }
	t.Cleanup(func() { inviteRegistry = previous })
}

// PrintWorkerCheckForTest prints what dagu worker --check found at one
// coordinator.
var PrintWorkerCheckForTest = printWorkerCheck

// EgressProxyForTest returns the egress-proxy command on the given listen and
// dial functions.
func EgressProxyForTest(listen func(network, address string) (net.Listener, error), dial egress.DialFunc) *cobra.Command {
	return egressProxy(listen, dial)
}

// CloudReportLeaseForTest returns the lease a server holds while it reports
// the DAG runs of cfg's data directory.
func CloudReportLeaseForTest(cfg *config.Config) *filemonitor.Lease {
	return cloudReportLease(cfg)
}
