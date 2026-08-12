// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type RepositoryTest struct {
	Context    context.Context
	Repository *persis.DAGRunRepository
	Backend    *Store
	TmpDir     string
}

func setupTestRepository(t *testing.T) RepositoryTest {
	tmpDir, err := os.MkdirTemp("", "test")
	require.NoError(t, err)

	backend := NewStore(tmpDir, WithArtifactDir(filepath.Join(tmpDir, "artifacts")))
	th := RepositoryTest{
		Context: context.Background(),
		Repository: persis.NewDAGRunRepository(backend, NewDAGRunWorkspaceStore(tmpDir), persis.DAGRunRepositoryOptions{
			LatestStatusToday: true,
			Location:          time.Local,
		}),
		Backend: backend,
		TmpDir:  tmpDir,
	}

	t.Cleanup(func() {
		_ = os.RemoveAll(th.TmpDir)
	})
	return th
}

func (th RepositoryTest) CreateAttempt(t *testing.T, ts time.Time, dagRunID string, s ir.Status) *Attempt {
	t.Helper()

	dag := th.DAG("test_DAG")
	return th.CreateAttemptWithDAG(t, ts, dagRunID, s, dag.DAG)
}

func (th RepositoryTest) CreateAttemptWithDAG(t *testing.T, ts time.Time, dagRunID string, s ir.Status, dag *ir.DAG) *Attempt {
	t.Helper()

	attempt, err := th.Repository.CreateAttempt(th.Context, dag, ts, dagRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)

	err = attempt.Open(th.Context)
	require.NoError(t, err)

	defer func() {
		_ = attempt.Close(th.Context)
	}()

	dagRunStatus := ir.InitialStatus(dag)
	dagRunStatus.DAGRunID = dagRunID
	dagRunStatus.Status = s

	err = attempt.Write(th.Context, dagRunStatus)
	require.NoError(t, err)

	concrete, ok := attempt.(*Attempt)
	require.True(t, ok, "expected *Attempt, got %T", attempt)
	return concrete
}

func (th RepositoryTest) DAG(name string) DAGTest {
	return DAGTest{
		th: th,
		DAG: &ir.DAG{
			Name:     name,
			Location: filepath.Join(th.TmpDir, name+".yaml"),
		},
	}
}

type DAGTest struct {
	th RepositoryTest
	*ir.DAG
}

func (d DAGTest) Writer(t *testing.T, dagRunID string, startedAt time.Time) WriterTest {
	t.Helper()

	root := NewDataRoot(d.th.TmpDir, d.Name)
	dagRun, err := root.CreateDAGRun(persis.NewUTC(startedAt), dagRunID)
	require.NoError(t, err)

	attempt, err := dagRun.CreateAttempt(d.th.Context, persis.NewUTC(startedAt), d.th.Backend.cache, "")
	require.NoError(t, err)
	attempt.SetDAG(d.DAG)

	writer := NewWriter(attempt.file)
	require.NoError(t, writer.Open())

	t.Cleanup(func() {
		require.NoError(t, writer.close())
	})

	return WriterTest{
		th: d.th,

		DAGRunID: dagRunID,
		FilePath: attempt.file,
		Writer:   writer,
	}
}

func (w WriterTest) Write(t *testing.T, dagRunStatus ir.DAGRunStatus) {
	t.Helper()

	err := w.Writer.write(dagRunStatus)
	require.NoError(t, err)
}

func (w WriterTest) AssertContent(t *testing.T, name, dagRunID string, st ir.Status) {
	t.Helper()

	data, err := ParseStatusFile(w.FilePath)
	require.NoError(t, err)

	assert.Equal(t, name, data.Name)
	assert.Equal(t, dagRunID, data.DAGRunID)
	assert.Equal(t, st, data.Status)
}

func (w WriterTest) Close(t *testing.T) {
	t.Helper()

	require.NoError(t, w.Writer.close())
}

type WriterTest struct {
	th RepositoryTest

	DAGRunID string
	FilePath string
	Writer   *Writer
	Closed   bool
}
