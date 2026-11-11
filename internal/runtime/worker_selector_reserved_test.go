// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/stretchr/testify/assert"
)

// Spec 092 "Reserved labels": Dagu sets the labels that
// start with agent. A step selector key that is a value reference resolves at
// run time. The resolved key must not start with agent., or a parameter sends
// a child that needs no provider to an agent worker.
func TestResolveWorkerSelectorRefusesReservedKey(t *testing.T) {
	t.Parallel()

	scope := cmnvalue.NewEnvScope(nil, false).WithEntry("K", "agent.provider.claude", cmnvalue.EnvSourceParam)
	resolved, err := resolveWorkerSelector(context.Background(), scope, map[string]string{"${K}": "true"})
	if err == nil {
		assert.Failf(t, "the runtime resolved a reserved selector key with no check", "selector %v", resolved)
	}
}
