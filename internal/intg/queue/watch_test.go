// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG that watches a folder runs once for each file that arrives there,
// with the file as WATCH_FILE and WATCH_NAME, through the scheduler the
// command starts: the folder is read, the file is given time to hold
// still, and its run is queued and processed.
func TestWatchedFolderStartsARunPerFile(t *testing.T) {
	inbox := t.TempDir()
	done := filepath.Join(t.TempDir(), "done")
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "already-there.txt"), []byte("old"), 0o600))

	f := newFixture(t, `
name: inbox
watch:
  path: `+yamlString(inbox)+`
  pattern: "*.txt"
  interval: 1s
steps:
  - id: keep
    action: file.copy
    with:
      source: ${WATCH_FILE}
      destination: `+yamlString(filepath.ToSlash(done)+"/${WATCH_NAME}")+`
      create_dirs: true
`).StartScheduler(60 * time.Second)
	defer f.Stop()

	// The scheduler reads the folder before the file arrives, so the file
	// already there is left alone and the new one is seen arriving.
	f.h.Wait.EventuallyEveryWithin("the folder is read", 20*time.Second, 200*time.Millisecond, func() bool {
		record, err := f.th.Backend.Collection(persis.CollectionWatchState).Get(f.th.Context, "watch")
		return err == nil && strings.Contains(string(record.Data), `"inbox"`)
	})
	require.NoError(t, os.WriteFile(filepath.Join(inbox, "order-1.txt"), []byte("new"), 0o600))

	var statuses []ir.DAGRunStatus
	f.h.Wait.EventuallyEveryWithin("the file's run finishes", 40*time.Second, 200*time.Millisecond, func() bool {
		var err error
		statuses, err = f.th.DAGRunRepository.RecentStatuses(f.th.Context, "inbox", 5)
		return err == nil && len(statuses) > 0 && statuses[0].Status != ir.Queued && statuses[0].Status != ir.Running
	})
	require.Len(t, statuses, 1, "one run for the file that arrived, none for the one already there")
	for _, node := range statuses[0].Nodes {
		assert.Empty(t, node.Error, "step %s", node.Step.Name)
	}
	assert.Equal(t, ir.Succeeded, statuses[0].Status)
	assert.Equal(t, ir.TriggerTypeFile, statuses[0].TriggerType)
	data, err := os.ReadFile(filepath.Join(done, "order-1.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))
	_, err = os.Stat(filepath.Join(done, "already-there.txt"))
	assert.True(t, os.IsNotExist(err))
}

// yamlString quotes a path for YAML, backslashes included.
func yamlString(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}
