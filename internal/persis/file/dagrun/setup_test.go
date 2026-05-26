// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type StoreTest struct {
	Context context.Context
	Store   exec.DAGRunStore
	TmpDir  string
}

func setupTestStore(t *testing.T) StoreTest {
	tmpDir, err := os.MkdirTemp("", "test")
	require.NoError(t, err)

	th := StoreTest{
		Context: context.Background(),
		Store:   New(tmpDir, WithArtifactDir(filepath.Join(tmpDir, "artifacts"))),
		TmpDir:  tmpDir,
	}

	t.Cleanup(func() {
		_ = os.RemoveAll(th.TmpDir)
	})
	return th
}

func (th StoreTest) CreateAttempt(t *testing.T, ts time.Time, dagRunID string, s core.Status) *Attempt {
	t.Helper()

	dag := th.DAG("test_DAG")
	return th.CreateAttemptWithDAG(t, ts, dagRunID, s, dag.DAG)
}

func (th StoreTest) CreateAttemptWithDAG(t *testing.T, ts time.Time, dagRunID string, s core.Status, dag *core.DAG) *Attempt {
	t.Helper()

	attempt, err := th.Store.CreateAttempt(th.Context, dag, ts, dagRunID, exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	err = attempt.Open(th.Context)
	require.NoError(t, err)

	defer func() {
		_ = attempt.Close(th.Context)
	}()

	dagRunStatus := exec.InitialStatus(dag)
	dagRunStatus.DAGRunID = dagRunID
	dagRunStatus.Status = s

	err = attempt.Write(th.Context, dagRunStatus)
	require.NoError(t, err)

	return attempt.(*Attempt)
}

func (th StoreTest) DAG(name string) DAGTest {
	return DAGTest{
		th: th,
		DAG: &core.DAG{
			Name:     name,
			Location: filepath.Join(th.TmpDir, name+".yaml"),
		},
	}
}

type DAGTest struct {
	th StoreTest
	*core.DAG
}

func (d DAGTest) Writer(t *testing.T, dagRunID string, startedAt time.Time) WriterTest {
	t.Helper()

	root := NewDataRoot(d.th.TmpDir, d.Name)
	dagRun, err := root.CreateDAGRun(exec.NewUTC(startedAt), dagRunID)
	require.NoError(t, err)

	store := d.th.Store.(*Store)
	attempt, err := dagRun.CreateAttempt(d.th.Context, exec.NewUTC(startedAt), store.cache, "", WithDAG(d.DAG))
	require.NoError(t, err)

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

func (w WriterTest) Write(t *testing.T, dagRunStatus exec.DAGRunStatus) {
	t.Helper()

	err := w.Writer.write(dagRunStatus)
	require.NoError(t, err)
}

func (w WriterTest) AssertContent(t *testing.T, name, dagRunID string, st core.Status) {
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
	th StoreTest

	DAGRunID string
	FilePath string
	Writer   *Writer
	Closed   bool
}
