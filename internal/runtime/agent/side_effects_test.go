// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/docker"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// connCounter is a TCP endpoint that counts the connections it accepts. It
// closes each one at once, so a client returns only after it was counted.
type connCounter struct {
	addr  string
	count atomic.Int32
}

func startConnCounter(t *testing.T) *connCounter {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	counter := &connCounter{addr: listener.Addr().String()}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			counter.count.Add(1)
			_ = conn.Close()
		}
	}()
	return counter
}

// A failing test run with no real step reaches no SMTP server, OTLP
// collector, or container daemon, and records the mail it would have sent.
// The step names a host executor, so it runs without the DAG container.
func TestAgent_TestRunSideEffects(t *testing.T) {
	smtp := startConnCounter(t)
	collector := startConnCounter(t)
	daemon := startConnCounter(t)
	t.Setenv(docker.ContainerRuntimeEnv, "")
	t.Setenv("DOCKER_HOST", "tcp://"+daemon.addr)

	smtpHost, smtpPort, err := net.SplitHostPort(smtp.addr)
	require.NoError(t, err)
	th := test.Setup(t)
	dag := th.DAG(t, fmt.Sprintf(`smtp:
  host: %s
  port: "%s"
mail_on:
  failure: true
error_mail:
  from: dagu@example.com
  to: ops@example.com
otel:
  enabled: true
  endpoint: http://%s/v1/traces
container:
  image: alpine
steps:
  - name: check
    type: command
    command: exit 1
    mail_on_error: true
`, smtpHost, smtpPort, collector.addr))
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
		TestSideEffects: &agent.TestSideEffects{},
	}))
	dagAgent.RunError(t)

	require.Equal(t, ir.NodeFailed, dagAgent.Status(th.Context).Nodes[0].Status)
	require.Zero(t, smtp.count.Load(), "mail sent")
	require.Zero(t, collector.count.Load(), "span exported")
	require.Zero(t, daemon.count.Load(), "container daemon reached")
	require.Equal(t, []agent.Notification{
		{Event: agent.NotificationFailure, Step: "check"},
		{Event: agent.NotificationFailure},
	}, dagAgent.Notifications())
}

// A test run creates the explicit working directory and the DAG-level SSH
// client, and resolves the SSH passphrase given as a secret reference, only
// when it has a real step. The SSH key file is missing, so creating the
// client fails.
func TestAgent_TestRunRealSteps(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		hasRealSteps bool
		wantErr      string
		wantResolves int
	}{
		{name: "NoRealSteps"},
		{name: "RealSteps", hasRealSteps: true, wantErr: "failed to create ssh client", wantResolves: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			resolver := newCountingSecretResolver(map[string]string{"ssh/passphrase": "passphrase-value"})
			workDir := filepath.Join(t.TempDir(), "work")
			dag := th.DAG(t, fmt.Sprintf(`working_dir: %q
ssh:
  user: deploy
  host: 127.0.0.1
  key: %q
  passphrase: {secret: ssh/passphrase}
steps:
  - name: check
    action: noop
`, workDir, filepath.Join(t.TempDir(), "missing-key")))
			dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
				SecretReferenceResolver: resolver,
				TestSideEffects:         &agent.TestSideEffects{HasRealSteps: tc.hasRealSteps},
			}))

			if tc.wantErr == "" {
				dagAgent.RunSuccess(t)
			} else {
				dagAgent.RunCheckErr(t, tc.wantErr)
			}
			_, statErr := os.Stat(workDir)
			require.Equal(t, tc.hasRealSteps, statErr == nil, "working directory created")
			require.Equal(t, tc.wantResolves, resolver.callCount("ssh/passphrase"), "passphrase resolved")
		})
	}
}

// A test run applies native resource limits only when it has a real step.
// Applying them logs either that they are enforced or why they are not, so a
// run that applies none logs neither.
func TestAgent_TestRunResourceLimits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		hasRealSteps bool
	}{
		{name: "NoRealSteps"},
		{name: "RealSteps", hasRealSteps: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t, test.WithCaptureLoggingOutput())
			dag := th.DAG(t, `resources:
  limits:
    memory: "1Gi"
steps:
  - name: check
    action: noop
`)
			dag.Agent(test.WithAgentOptions(agent.Options{
				TestSideEffects: &agent.TestSideEffects{HasRealSteps: tc.hasRealSteps},
			})).RunSuccess(t)

			logs := th.LoggingOutput.String()
			require.Equal(t, tc.hasRealSteps, strings.Contains(logs, "resource limits"), logs)
		})
	}
}
