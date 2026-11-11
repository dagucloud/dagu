// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/dagucloud/dagu/v2/internal/spec"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

// A definition has no file on the coordinator. The coordinator finds the skill
// of the DAG in its DAGs directory, so a DAG whose agent.run step has the CLI
// provider is dispatched and goes to the agent worker of that provider. The task
// carries a bundle with the skill for the worker.
func TestDispatchFindsTheSkillInTheDAGsDirectory(t *testing.T) {
	t.Parallel()

	dagsDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dagsDir, "skills", "review-notes"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "skills", "review-notes", "SKILL.md"),
		[]byte("---\nname: review-notes\ndescription: How to review\n---\nRead the diff first.\n"), 0o600))
	definition := "name: coder\nsteps:\n  - id: code\n    action: agent.run\n    with:\n      provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      skills: [review-notes]\n      prompt: hi\n"
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "coder.yaml"), []byte(definition), 0o600))
	dags := persis.NewDAGRepository(filedag.NewStore(dagsDir), persis.DAGRepositoryOptions{DAGsDir: dagsDir})

	s := startReviewServerWithDAGs(t, dags)
	agent := s.client(t, "agent-claude")
	dispatcher := s.client(t, "server")
	heartbeat(t, agent, "agent-1", agentLabels)

	_, err := dispatcher.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation:  coordinatorv1.Operation_OPERATION_START,
		DagRunId:   "skill-run",
		Target:     "coder",
		Definition: definition,
	}})
	require.NoError(t, err)

	task, err := poll(t, agent, "agent-1", agentLabels)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, "true", task.WorkerSelector["agent.provider.claude"])
	// The dispatcher sent no bundle. The coordinator made one with the skill.
	require.NotEmpty(t, task.WorkspaceBundleDigest)
}

// The bundle of a dispatch holds the whole folder of the skill and its version.
// The worker gets exactly that version: an edit of the folder after the dispatch
// changes nothing for the worker, and the worker never reads the DAGs directory
// of the coordinator (Spec 096, "The bundle").
func TestDispatchBundleCarriesTheSkillVersion(t *testing.T) {
	t.Parallel()

	dagsDir := t.TempDir()
	folder := filepath.Join(dagsDir, "skills", "review-notes")
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "references"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(folder, "assets"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "SKILL.md"),
		[]byte("---\nname: review-notes\ndescription: How to review a change. Use when reviewing.\n---\nRead the diff first.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "references", "checklist.md"), []byte("# Checklist\n1. tests\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "assets", "logo.bin"), []byte{0, 1, 2, 255}, 0o600))
	definition := "name: coder\nsteps:\n  - id: code\n    action: agent.run\n    with:\n      provider: claude\n      endpoint:\n        name: test-endpoint\n        model: claude-test\n      skills: [review-notes]\n      prompt: hi\n"
	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "coder.yaml"), []byte(definition), 0o600))
	dags := persis.NewDAGRepository(filedag.NewStore(dagsDir), persis.DAGRepositoryOptions{DAGsDir: dagsDir})

	before, err := skill.Read(folder)
	require.NoError(t, err)
	require.True(t, before.Valid())

	s := startReviewServerWithDAGs(t, dags)
	agent := s.client(t, "agent-claude")
	dispatcher := s.client(t, "server")
	heartbeat(t, agent, "agent-1", agentLabels)
	_, err = dispatcher.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{Task: &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_START, DagRunId: "skill-run", Target: "coder", Definition: definition,
	}})
	require.NoError(t, err)
	task, err := poll(t, agent, "agent-1", agentLabels)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.NotEmpty(t, task.WorkspaceBundleDigest)

	// The skill changes on the coordinator after the dispatch.
	require.NoError(t, os.WriteFile(filepath.Join(folder, "references", "checklist.md"), []byte("# Changed\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(folder, "SKILL.md"),
		[]byte("---\nname: review-notes\ndescription: How to review a change. Use when reviewing.\n---\nChanged body.\n"), 0o600))

	// The worker reads the bundle, not the folder.
	bundles := workspacebundle.NewStore(s.bundleDir, workspacebundle.DefaultLimits())
	data, err := bundles.Get(context.Background(), task.WorkspaceBundleDigest)
	require.NoError(t, err)
	workspace := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, workspacebundle.Extract(data, workspace, workspacebundle.Descriptor{
		Digest: task.WorkspaceBundleDigest, Size: task.WorkspaceBundleSize, DAGPath: task.WorkspaceBundleDagPath,
	}, workspacebundle.DefaultLimits()))
	dag, err := spec.Load(context.Background(), filepath.Join(workspace, task.WorkspaceBundleDagPath))
	require.NoError(t, err)
	entry := dag.Steps[0].Agent.CLI.Skills[0]
	require.Equal(t, before.Hash, entry.Hash, "the worker has the version of the dispatch")
	require.Equal(t, filepath.Join(workspace, "skills", "review-notes"), entry.Dir)
	require.Equal(t, "Read the diff first.\n", entry.Body)
	var paths []string
	for _, f := range entry.Files {
		paths = append(paths, f.Path)
		if f.Path == "references/checklist.md" {
			require.NotNil(t, f.Text)
			require.Equal(t, "# Checklist\n1. tests\n", *f.Text)
		}
	}
	require.Equal(t, []string{"SKILL.md", "assets/logo.bin", "references/checklist.md"}, paths)
	now, err := skill.Read(folder)
	require.NoError(t, err)
	require.NotEqual(t, before.Hash, now.Hash, "the folder changed meanwhile")
}
