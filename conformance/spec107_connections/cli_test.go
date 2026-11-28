// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec107_connections_test

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const (
	cliClientID     = "123-abc.apps.googleusercontent.com"
	cliClientSecret = "GOCSPX-conformance-secret"
)

// dagu connection calls the REST API of the server that this host's
// configuration describes (Spec 107 §CLI). With a desktop client and no
// browser, add registers the client file, creates the connection, and
// shows the redirect URI to register and the client ID before consent. A
// sign-in that gets no pasted address is cancelled on the server, and the
// connection stays pending until remove deletes it. No output holds the
// client secret.
func TestConnectionCLI(t *testing.T) {
	t.Parallel()

	_, home, store := newHome(t)
	dagu := harness.NewRunner(t)
	port := harness.FreePort(t)
	env := append(home, "DAGU_AUTH_MODE=none", "DAGU_HOST=127.0.0.1", fmt.Sprintf("DAGU_PORT=%d", port))
	server := dagu.StartWithEnv(env, "server")
	defer server.Stop()
	waitForListener(t, server, fmt.Sprintf("127.0.0.1:%d", port))

	dagu.WriteFile("client.json", fmt.Sprintf(`{"installed":{"client_id":%q,"client_secret":%q,"redirect_uris":["http://localhost"]}}`,
		cliClientID, cliClientSecret))
	add := dagu.RunWithStdin(env, strings.NewReader(""), "connection", "add", "google", "work",
		"--services", "gmail", "--client-file", dagu.ProjectPath("client.json"), "--no-browser")
	add.ExpectNonZeroExitCode()
	for _, want := range []string{
		"Register this redirect URI in the client: http://127.0.0.1:1/oauth/callback",
		"Client ID: " + cliClientID,
		"https://accounts.google.com/",
	} {
		require.Contains(t, add.Stdout(), want)
	}
	add.ExpectStderrContains("sign-in cancelled", "Connection work stays pending")
	add.ExpectStdoutNotContains(cliClientSecret)
	add.ExpectStderrNotContains(cliClientSecret)

	ctx := context.Background()
	conn, err := store.Get(ctx, connection.Key{Workspace: secret.GlobalWorkspace, Name: "work"})
	require.NoError(t, err)
	state, err := store.TokenState(ctx, conn)
	require.NoError(t, err)
	require.Equal(t, connection.TokenStatusPending, state.Status)
	require.NotNil(t, state.Session)
	require.Equal(t, connection.SessionPhaseFailed, state.Session.Phase)
	require.Equal(t, "cancelled", state.Session.Reason)

	remove := dagu.RunWithEnv(env, "connections", "remove", "work", "--yes")
	remove.ExpectExitCode(0)
	require.Contains(t, remove.Stdout(), "Connection work was removed.")

	reconnect := dagu.RunWithEnv(env, "connection", "reconnect", "work")
	reconnect.ExpectNonZeroExitCode()
	reconnect.ExpectStderrContains("Connection not found")
}

func waitForListener(t *testing.T, process *harness.Process, address string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("server exited during startup: %s", process.FailureOutput())
		default:
		}
		if conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
			require.NoError(t, conn.Close())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not listen on %s", address)
}
