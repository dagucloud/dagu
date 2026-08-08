// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

type deadlineProvider interface {
	Deadline() (time.Time, bool)
}

func RunBuiltCLICommand(tb testing.TB, th Helper, extraEnv []string, args ...string) ([]byte, error) {
	tb.Helper()

	ctx := context.Background()
	cancel := func() {}
	if provider, ok := any(tb).(deadlineProvider); ok {
		if deadline, ok := provider.Deadline(); ok {
			ctx, cancel = context.WithDeadline(ctx, deadline)
		}
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, th.Config.Paths.Executable, WithConfigFlag(args, th.Config)...) //nolint:gosec // Test helper executes the built dagu binary from the harness config.
	cmd.Env = append(append([]string{}, th.ChildEnv...), extraEnv...)
	return cmd.CombinedOutput()
}

func RunBuiltCLI(t *testing.T, th Helper, extraEnv []string, args ...string) string {
	t.Helper()

	output, err := RunBuiltCLICommand(t, th, extraEnv, args...)
	require.NoError(t, err, "output: %s", string(output))
	return string(output)
}

func StatusOutputValue(t *testing.T, status *ir.DAGRunStatus, key string) string {
	t.Helper()

	require.NotNil(t, status)
	for _, node := range status.Nodes {
		if node.OutputVariables == nil {
			continue
		}
		value, ok := node.OutputVariables.Load(key)
		if ok {
			result, ok := value.(string)
			require.True(t, ok, "output %q has unexpected type %T", key, value)
			return strings.TrimPrefix(result, key+"=")
		}
	}

	t.Fatalf("output %q not found in DAG-run status", key)
	return ""
}
