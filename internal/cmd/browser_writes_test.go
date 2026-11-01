// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runWritesCommand(t *testing.T, th test.Command, args ...string) map[string]any {
	t.Helper()
	var out bytes.Buffer
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("context", "", "")
	root.AddCommand(cmd.Browser())
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(test.WithConfigFlag(append([]string{"browser", "writes"}, args...), th.Config))
	require.NoError(t, root.ExecuteContext(th.Context), out.String())
	var printed map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &printed), out.String())
	return printed
}

// A rehearsal's commit points are approved into the DAG that runs for
// real, and revoked from it.
func TestBrowserWritesCommands(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	store := writegate.NewCommitStore(filepath.Join(th.Config.Paths.DataDir, browserhost.DataDirName))
	save := writegate.CommitPoint{Key: "k1", Op: 1, Operation: "Click Save", Signature: "POST https://books.example.com/api/deals? {amount}"}
	attach := writegate.CommitPoint{Key: "k2", Op: 2, Operation: "Click Attach", Signature: "POST https://books.example.com/api/files? {file(file)}"}
	require.NoError(t, store.Note(th.Context, "books-trial", "enter", save, attach))

	approved := runWritesCommand(t, th, "approve", "books", "--from-dag", "books-trial", "--signature", save.Signature)
	assert.Equal(t, []any{map[string]any{"step": "enter", "op": 1.0, "operation": "Click Save", "signature": save.Signature}}, approved["approved"])
	points, err := store.Load("books", "enter")
	require.NoError(t, err)
	assert.Equal(t, []string{save.Signature}, writegate.Allowed(points, "k1"))
	assert.Len(t, points, 1, "only the chosen point is copied")

	listed := runWritesCommand(t, th, "list", "books-trial")
	steps, _ := listed["steps"].(map[string]any)
	assert.Len(t, steps["enter"], 2)

	revoked := runWritesCommand(t, th, "revoke", "books", "--step", "enter")
	assert.Len(t, revoked["revoked"], 1)
	points, err = store.Load("books", "enter")
	require.NoError(t, err)
	assert.Empty(t, writegate.Allowed(points, "k1"))
}
