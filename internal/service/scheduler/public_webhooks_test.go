// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/publicwebhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type profileResolverFunc func() (string, error)

func (f profileResolverFunc) ResolveProfile(context.Context, string, string) (string, error) {
	return f()
}

// A DAG whose default profile cannot run fails its deliveries; a profile
// store that cannot be read only delays them.
func TestWebhookRunsProfileErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		labels  []string
		err     error
		invalid bool
	}{
		{name: "disabled profile", err: fmt.Errorf("runtime profile prod: %w", profile.ErrDisabled), invalid: true},
		{name: "missing profile", err: fmt.Errorf("runtime profile prod: %w", profile.ErrNotFound), invalid: true},
		{name: "invalid workspace label", labels: []string{"workspace=bad/name"}, invalid: true},
		{name: "unreadable profile store", err: errors.New("read profiles: input/output error")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runs := &webhookRuns{dagExecutor: NewDAGExecutor(nil, nil, "", "",
				WithDAGExecutorProfileResolver(profileResolverFunc(func() (string, error) { return "", tc.err })))}
			dag := &ir.DAG{Name: "ci", Labels: ir.NewLabels(tc.labels)}

			err := runs.Enqueue(context.Background(), "ci.yaml", dag, "delivery-1", "")
			require.Error(t, err)
			assert.Equal(t, tc.invalid, errors.Is(err, publicwebhook.ErrInvalidRun), err)
		})
	}
}
