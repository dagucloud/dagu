// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateDAG has a step with a text, a multi-line, and a type: json output,
// two more steps, and a handler.
const updateDAG = `steps:
  - name: build image
    id: build
    run: ./build.sh
    outputs:
      - name: tag
      - name: notes
      - name: meta
        type: json
  - id: test
    run: ./test.sh
  - id: deploy
    run: ./deploy.sh
handler_on:
  exit:
    run: ./cleanup.sh
`

// Set changes only the text of what changes, in the file's indentation:
// values keep their comments and quotes, an object keeps the keys whose
// values stay, a multi-line string becomes a block scalar, added steps
// follow the last step entry, and replaced notifications are written in
// full. The other case, the blank lines, and every comment stay.
func TestUpdate(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, updateDAG, `# Release tests
mocks:
    build:
        outputs: {tag: v1, notes: "", meta: {}}
    test: {}
    deploy: {}
    handler_on.exit: {}

cases:
    - name: stale   # a stale case
      expect:
          status: 'succeeded'  # was green
          attempts: 1
          steps:
              build image:
                  status: succeeded
                  outputs:
                      tag: v1   # the tag
                      notes: one line
                      meta:
                          version: 1
                          labels: [a]   # kept
                  stdout:
                      contains: built
              # the test step
              test: succeeded

          notifications:
              - kind: mail_on.success

    - name: same
      expect:
          status: succeeded
`)
	update, file := newTestUpdate(t, path)
	changed, err := update.Set("stale", updatedExpect(file.Cases[0].Expect, func(e *Expect) {
		e.Status, e.Attempts = "failed", new(2)
		build := &e.Steps[0]
		build.Status = "failed"
		build.Outputs[0].Value = "v2"
		build.Outputs[1].Value = "line one\nline two"
		meta := build.Outputs[2].Value.(map[string]any)
		build.Outputs[2].Value = map[string]any{"version": int64(2), "labels": meta["labels"]}
		e.Steps[1].Status = "aborted"
		e.Steps = append(e.Steps,
			StepExpect{Key: "deploy", Step: "deploy", Status: "aborted"},
			StepExpect{Key: "handler_on.exit", Handler: ir.HandlerOnExit, Status: "succeeded", Attempts: new(2)})
		e.Notifications = []Notification{{Kind: "mail_on_error", Step: "build image", To: []string{"ops@example.com"}}}
	}))
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = update.Set("same", file.Cases[1].Expect)
	require.NoError(t, err)
	assert.False(t, changed)
	require.NoError(t, update.Write())

	assertFile(t, path, `# Release tests
mocks:
    build:
        outputs: {tag: v1, notes: "", meta: {}}
    test: {}
    deploy: {}
    handler_on.exit: {}

cases:
    - name: stale   # a stale case
      expect:
          status: 'failed'  # was green
          attempts: 2
          steps:
              build image:
                  status: failed
                  outputs:
                      tag: v2   # the tag
                      notes: |-
                          line one
                          line two
                      meta:
                          version: 2
                          labels: [a]   # kept
                  stdout:
                      contains: built
              # the test step
              test: aborted
              deploy: aborted
              handler_on.exit:
                  status: succeeded
                  attempts: 2

          notifications:
              - kind: mail_on_error
                step: build
                to:
                  - ops@example.com

    - name: same
      expect:
          status: succeeded
`)
	loaded, err := LoadFile(context.Background(), path)
	require.NoError(t, err)
	assert.Empty(t, loaded.Cases[0].Errors)
}

// A value or a map that Set cannot change in its old text is written anew
// under its key, and a comment on the key's line stays there.
func TestUpdateRewrites(t *testing.T) {
	t.Parallel()

	const head = "mocks:\n  build: {}\n  test: {}\n  deploy: {}\ncases:\n  - name: c\n"
	tests := []struct {
		name   string
		expect string
		edit   func(*Expect)
		want   string
		// output, when set, is the value build's first output reads back
		// as from the written file.
		output any
	}{
		{
			name:   "FlowExpect",
			expect: "    expect: {status: succeeded}  # first draft\n",
			edit: func(e *Expect) {
				e.Status = "failed"
				e.Steps = []StepExpect{{Key: "build", Step: "build image", Status: "failed"}}
			},
			want: "    expect:  # first draft\n      status: failed\n      steps:\n        build: failed\n",
		},
		{
			name:   "NoSteps",
			expect: "    expect:\n      status: succeeded\n",
			edit: func(e *Expect) {
				e.Steps = []StepExpect{{Key: "build", Step: "build image", Status: "succeeded"}}
			},
			want: "    expect:\n      status: succeeded\n      steps:\n        build: succeeded\n",
		},
		{
			name:   "NotificationsNone",
			expect: "    expect:\n      notifications:  # sent\n      - kind: mail_on.success\n",
			edit:   func(e *Expect) { e.Notifications = []Notification{} },
			want:   "    expect:\n      notifications: []  # sent\n",
		},
		{
			name:   "NotificationsSent",
			expect: "    expect:\n      notifications: []\n      status: failed\n",
			edit:   func(e *Expect) { e.Notifications = []Notification{{Kind: "mail_on.failure"}} },
			want:   "    expect:\n      notifications:\n        - kind: mail_on.failure\n      status: failed\n",
		},
		{
			name:   "ScalarToObject",
			expect: "    expect:\n      steps:\n        build:\n          outputs:\n            meta: 3  # count\n",
			edit: func(e *Expect) {
				e.Steps[0].Outputs[0].Value = map[string]any{"files": int64(3)}
			},
			want: "    expect:\n      steps:\n        build:\n          outputs:\n            meta:  # count\n              files: 3\n",
		},
		{
			name:   "MultilineFlow",
			expect: "    expect: {\n      # why\n      status: succeeded # rationale\n    }\n# end\n",
			edit: func(e *Expect) {
				e.Steps = []StepExpect{{Key: "build", Step: "build image", Status: "succeeded"}}
			},
			want: "    expect:\n      # why\n      status: succeeded # rationale\n      steps:\n        build: succeeded\n# end\n",
		},
		{
			name:   "BigInteger",
			expect: "    expect:\n      steps:\n        build:\n          outputs:\n            tag: 1\n",
			edit: func(e *Expect) {
				e.Steps[0].Outputs[0].Value = uint64(9223372036854775809)
			},
			want:   "    expect:\n      steps:\n        build:\n          outputs:\n            tag: 9223372036854775809\n",
			output: uint64(9223372036854775809),
		},
		{
			name:   "CRLF",
			expect: "    expect:\r\n      status: succeeded\r\n      steps:\r\n        build: succeeded\r\n",
			edit: func(e *Expect) {
				e.Status = "failed"
				e.Steps = append(e.Steps, StepExpect{Key: "test", Step: "test", Status: "aborted"})
			},
			want: "    expect:\r\n      status: failed\r\n      steps:\r\n        build: succeeded\r\n        test: aborted\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := writeTestFile(t, updateDAG, head+tt.expect)
			update, file := newTestUpdate(t, path)
			changed, err := update.Set("c", updatedExpect(file.Cases[0].Expect, tt.edit))
			require.NoError(t, err)
			assert.True(t, changed)
			require.NoError(t, update.Write())
			assertFile(t, path, head+tt.want)
			if tt.output != nil {
				written, err := LoadFile(context.Background(), path)
				require.NoError(t, err)
				assert.Equal(t, tt.output, written.Cases[0].Expect.Steps[0].Outputs[0].Value)
			}
		})
	}
}

// An edit through an anchor or alias would change another part of the
// file, so Set keeps the expect of a case that uses one, a merge key, or
// flow style, and Write leaves the file as it is.
func TestUpdateKeepsForm(t *testing.T) {
	t.Parallel()

	for name, cases := range map[string]string{
		"Anchor":   "  - name: c\n    expect: &e\n      status: succeeded\n  - name: d\n    expect: *e\n",
		"Alias":    "  - name: d\n    expect:\n      status: &s succeeded\n  - name: c\n    expect:\n      status: *s\n",
		"MergeKey": "  - name: c\n    expect:\n      <<: {status: succeeded}\n      attempts: 1\n",
		"FlowCase": "  - {name: c, expect: {status: succeeded}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			file := "mocks:\n  build: {}\ncases:\n" + cases
			path := writeTestFile(t, draftDAG, file)
			update, loaded := newTestUpdate(t, path)
			i := slices.IndexFunc(loaded.Cases, func(c Case) bool { return c.Name == "c" })
			changed, err := update.Set("c", updatedExpect(loaded.Cases[i].Expect, func(e *Expect) { e.Status = "failed" }))
			require.ErrorIs(t, err, errCaseForm)
			assert.False(t, changed)
			require.NoError(t, update.Write())
			assertFile(t, path, file)
		})
	}
}

// Write keeps the file's mode, writes nothing when no expect changed, and
// writes nothing when the file changed since it was read.
func TestUpdateWrite(t *testing.T) {
	t.Parallel()

	const file = "mocks:\n  build: {}\ncases:\n  - name: c\n    expect:\n      status: succeeded\n"
	t.Run("Mode", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("Windows has no mode bits for group and others")
		}

		path := writeTestFile(t, draftDAG, file)
		require.NoError(t, os.Chmod(path, 0o640))
		update, loaded := newTestUpdate(t, path)
		_, err := update.Set("c", updatedExpect(loaded.Cases[0].Expect, func(e *Expect) { e.Status = "failed" }))
		require.NoError(t, err)
		require.NoError(t, update.Write())
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
		assertFile(t, path, "mocks:\n  build: {}\ncases:\n  - name: c\n    expect:\n      status: failed\n")
	})
	t.Run("Unchanged", func(t *testing.T) {
		t.Parallel()

		path := writeTestFile(t, draftDAG, file)
		update, loaded := newTestUpdate(t, path)
		require.NoError(t, os.Remove(path))
		_, err := update.Set("c", loaded.Cases[0].Expect)
		require.NoError(t, err)
		require.NoError(t, update.Write())
		assert.NoFileExists(t, path)
	})
	t.Run("ChangedMeanwhile", func(t *testing.T) {
		t.Parallel()

		path := writeTestFile(t, draftDAG, file)
		update, loaded := newTestUpdate(t, path)
		_, err := update.Set("c", updatedExpect(loaded.Cases[0].Expect, func(e *Expect) { e.Status = "failed" }))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(file+"# edited\n"), 0o600))
		require.ErrorContains(t, update.Write(), "changed while its cases ran")
		assertFile(t, path, file+"# edited\n")
	})
}

// writeTestFile writes a DAG and its test file under a new directory and
// returns the test file's path.
func writeTestFile(t *testing.T, dag, file string) string {
	t.Helper()

	dir := writeFiles(t, map[string]string{"release.yaml": dag, "release.test.yaml": file})
	return filepath.Join(dir, "release.test.yaml")
}

// newTestUpdate loads the test file at path and returns its update.
func newTestUpdate(t *testing.T, path string) (*Update, *File) {
	t.Helper()

	file, err := LoadFile(context.Background(), path)
	require.NoError(t, err)
	update, err := NewUpdate(file)
	require.NoError(t, err)
	return update, file
}

// updatedExpect returns a copy of written that edit changed. The copy
// shares no step or output with written.
func updatedExpect(written Expect, edit func(*Expect)) Expect {
	e := written
	e.Steps = slices.Clone(written.Steps)
	for i := range e.Steps {
		e.Steps[i].Outputs = slices.Clone(e.Steps[i].Outputs)
	}
	edit(&e)
	return e
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- the path is under the test's directory.
	require.NoError(t, err)
	assert.Equal(t, want, string(data))
}
