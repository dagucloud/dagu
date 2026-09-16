// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The artifact listing decides a child run's visibility by its root's
// workspace. This is the function that supplies it, against the real run
// store, so a wiring mistake cannot quietly turn every child invisible.
func TestRootLabelsFromRuns(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	store := filedagrun.NewStore(tmp, filedagrun.WithArtifactDir(filepath.Join(tmp, "artifacts")))
	repo := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
	ctx := context.Background()

	dag := &ir.DAG{
		Name:     "secret-dag",
		Location: filepath.Join(tmp, "secret-dag.yaml"),
		Labels:   ir.NewLabels([]string{"workspace=secret"}),
	}
	attempt, err := repo.CreateAttempt(ctx, dag, time.Now(), "secret-run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	status := ir.InitialStatus(dag)
	status.DAGRunID = "secret-run"
	status.Status = ir.Succeeded
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))

	resolve := file.RootLabelsFromRuns(repo)

	labels, found := resolve(ctx, ir.NewDAGRunRef("secret-dag", "secret-run"))
	require.True(t, found)
	assert.Equal(t, []string{"workspace=secret"}, labels)

	_, found = resolve(ctx, ir.NewDAGRunRef("secret-dag", "never-ran"))
	assert.False(t, found, "a run that does not exist must not resolve")
}
