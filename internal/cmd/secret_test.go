// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"io"
	"os"
	"os/user"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	persisstore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretResolve(t *testing.T) {
	th := test.SetupCommand(t)
	store := file.NewSecretStore(th.Context, th.Config, th.Backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, store)
	seed := func(workspace, ref string, value *string) *secret.Secret {
		t.Helper()
		sec, err := secret.New(secret.CreateInput{Workspace: workspace, Ref: ref, ProviderType: secret.ProviderDaguManaged}, time.Time{})
		require.NoError(t, err)
		var input *secret.WriteValueInput
		if value != nil {
			input = &secret.WriteValueInput{Value: *value}
		}
		require.NoError(t, store.Create(th.Context, sec, input))
		return sec
	}
	value := func(v string) *string { return &v }

	globalToken := seed("", "api/token", value("global-token \n"))
	seed("", "shared", value("global-shared"))
	teamToken := seed("team", "api/token", value("team-token"))
	disabled := seed("", "old", value("old-token"))
	disabled.Status = secret.StatusDisabled
	require.NoError(t, store.Update(th.Context, disabled))
	seed("", "empty", nil)
	seed("", "ai/endpoint/anthropic", value("provider-key"))
	workspaces, err := persisstore.NewWorkspaceStore(th.Backend.Collection(persis.CollectionWorkspaces))
	require.NoError(t, err)
	require.NoError(t, workspaces.Create(th.Context, workspace.NewWorkspace("ops", "")))

	t.Run("PrintsExactValue", func(t *testing.T) {
		out, err := runSecretResolve(th, "api/token")
		require.NoError(t, err)
		assert.Equal(t, "global-token \n", out)
	})

	t.Run("ResolvesInWorkspace", func(t *testing.T) {
		out, err := runSecretResolve(th, "api/token", "--workspace", "team")
		require.NoError(t, err)
		assert.Equal(t, "team-token", out)
	})

	// A workspace without the ref falls back to global, as a DAG run does.
	t.Run("FallsBackToGlobal", func(t *testing.T) {
		out, err := runSecretResolve(th, "shared", "--workspace", "team")
		require.NoError(t, err)
		assert.Equal(t, "global-shared", out)
	})

	// A registered workspace without its own secrets also falls back to global.
	t.Run("FallsBackInRegisteredWorkspace", func(t *testing.T) {
		out, err := runSecretResolve(th, "shared", "--workspace", "ops")
		require.NoError(t, err)
		assert.Equal(t, "global-shared", out)
	})

	// A mistyped workspace must not silently resolve the global value.
	t.Run("RejectsUnknownWorkspace", func(t *testing.T) {
		out, err := runSecretResolve(th, "api/token", "--workspace", "tema")
		require.ErrorIs(t, err, workspace.ErrWorkspaceNotFound)
		assert.Empty(t, out)
	})

	t.Run("DoesNotRecordResolution", func(t *testing.T) {
		_, err := runSecretResolve(th, "api/token")
		require.NoError(t, err)
		got, err := store.GetByID(th.Context, globalToken.ID)
		require.NoError(t, err)
		assert.Nil(t, got.LastResolvedAt)
	})

	t.Run("RecordsAuditEntry", func(t *testing.T) {
		_, err := runSecretResolve(th, "api/token", "--workspace", "team")
		require.NoError(t, err)
		auditStore, err := fileaudit.New(fileaudit.Dir(th.Config.Paths.AdminLogsDir), 0)
		require.NoError(t, err)
		result, err := auditStore.Query(th.Context, audit.QueryFilter{Category: audit.CategorySecret, ResourceID: teamToken.ID})
		require.NoError(t, err)
		require.NotEmpty(t, result.Entries)
		current, err := user.Current()
		require.NoError(t, err)
		entry := result.Entries[0]
		assert.Equal(t, "secret_resolve", entry.Action)
		assert.Equal(t, "cli", entry.Source)
		assert.Equal(t, "team", entry.Workspace)
		assert.Equal(t, current.Username, entry.Username)
	})

	for _, tc := range []struct {
		name string
		args []string
		want error
	}{
		{name: "Missing", args: []string{"absent"}, want: secret.ErrNotFound},
		{name: "Disabled", args: []string{"old"}, want: secret.ErrDisabled},
		{name: "NoValue", args: []string{"empty"}, want: secret.ErrNoValue},
		{name: "InvalidRef", args: []string{"Not A Ref"}, want: secret.ErrInvalidRef},
		{name: "ReservedRef", args: []string{"ai/endpoint/anthropic"}, want: secret.ErrReservedRef},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runSecretResolve(th, tc.args...)
			require.ErrorIs(t, err, tc.want)
			assert.Empty(t, out)
		})
	}

	t.Run("RejectsReservedWorkspace", func(t *testing.T) {
		_, err := runSecretResolve(th, "shared", "--workspace", "default")
		require.ErrorIs(t, err, workspace.ErrInvalidWorkspaceName)
	})

	t.Run("RejectsRemoteContext", func(t *testing.T) {
		_, err := runCommand(th, cmd.ContextCommand(), "context", "add", "prod", "--server", "https://dagu.example.com", "--api-key", "dagu_test")
		require.NoError(t, err)
		_, err = runSecretResolve(th, "shared", "--context", "prod")
		require.ErrorContains(t, err, "only supports the local context")
	})
}

// A read that cannot be audited must not reveal the value.
func TestSecretResolveRequiresAudit(t *testing.T) {
	th := test.SetupCommand(t)
	store := file.NewSecretStore(th.Context, th.Config, th.Backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, store)
	sec, err := secret.New(secret.CreateInput{Ref: "api/token", ProviderType: secret.ProviderDaguManaged}, time.Time{})
	require.NoError(t, err)
	require.NoError(t, store.Create(th.Context, sec, &secret.WriteValueInput{Value: "token"}))
	require.NoError(t, os.MkdirAll(th.Config.Paths.AdminLogsDir, 0o750))
	require.NoError(t, os.WriteFile(fileaudit.Dir(th.Config.Paths.AdminLogsDir), nil, 0o600))

	out, err := runSecretResolve(th, "api/token")
	require.Error(t, err)
	assert.Empty(t, out)
}

func runSecretResolve(th test.Command, args ...string) (string, error) {
	return runCommand(th, cmd.Secret(), append([]string{"secret", "resolve"}, args...)...)
}

// runCommand executes c under a root carrying the persistent --context flag and
// returns what it wrote to stdout.
func runCommand(th test.Command, c *cobra.Command, args ...string) (string, error) {
	var out bytes.Buffer
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("context", "", "")
	root.AddCommand(c)
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(test.WithConfigFlag(args, th.Config))
	err := root.ExecuteContext(th.Context)
	return out.String(), err
}
