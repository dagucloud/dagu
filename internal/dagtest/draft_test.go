// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// draftDAG is a DAG whose one step has side effects.
const draftDAG = "steps:\n  - id: build\n    run: ./build.sh\n"

// A draft handles each source of the fail-closed rule as the fix does,
// gives each required param without a default a value, and writes the
// expect in the shape of one written from a run. The written file passes
// the schema and loads with a valid case.
func TestNewDraft(t *testing.T) {
	t.Parallel()

	dir := writeFiles(t, map[string]string{"release.yaml": `
params:
  - name: region
    required: true
    enum: [eu, us]
  - name: env
    default: prod
  - name: stamp
    eval: $(date +%s)
preconditions:
  - condition: test -f ready
steps:
  - name: build image
    id: build
    run: ./build.sh
    outputs:
      - name: tag
  - name: notify
    action: log.write
    with:
      message: built
handler_on:
  failure:
    run: ./page.sh
`})
	draft, err := NewDraft(context.Background(), filepath.Join(dir, "release.yaml"))
	require.NoError(t, err)
	path := filepath.Join(dir, "release.test.yaml")
	require.Equal(t, path, draft.File.Path)
	require.Len(t, draft.File.Cases, 1)
	assert.Empty(t, draft.File.Cases[0].Errors)
	assert.Equal(t, "succeeded", draft.File.Cases[0].Expect.Status)

	require.NoError(t, draft.Write(Expect{
		Status:   "failed",
		Attempts: new(1),
		Steps: []StepExpect{
			{Key: "build", Step: "build image", Status: "failed", Attempts: new(2)},
			{Key: "notify", Step: "notify", Status: "not_started", Attempts: new(0)},
			{Key: "handler_on.failure", Handler: ir.HandlerOnFailure, Status: "succeeded", Attempts: new(1)},
		},
		Notifications: []Notification{{Kind: "mail_on_error", Step: "build image", To: []string{"oncall@example.com"}}},
	}))
	data, err := os.ReadFile(path) // #nosec G304 -- the path is under the test's directory.
	require.NoError(t, err)
	assert.Equal(t, `params:
  region: eu
  stamp: ""
mocks:
  build:
    outputs:
      tag: ""
  handler_on.failure: {}
cases:
  - name: initial
    precondition: met
    expect:
      status: failed
      steps:
        build:
          status: failed
          attempts: 2
        notify: not_started
        handler_on.failure: succeeded
      notifications:
        - kind: mail_on_error
          step: build
          to:
            - oncall@example.com
`, string(data))

	full, _ := resolveSchemas(t)
	require.NoError(t, validateYAML(full, data))
	file, err := LoadFile(context.Background(), path)
	require.NoError(t, err)
	require.Len(t, file.Cases, 1)
	assert.Empty(t, file.Cases[0].Errors)
}

// Without dag, the test file of foo.yml would test foo.yaml beside it, so
// the draft names its DAG.
func TestNewDraftNamesDAG(t *testing.T) {
	t.Parallel()

	dir := writeFiles(t, map[string]string{
		"twin.yaml": "steps:\n  - id: other\n    run: ./other.sh\n",
		"twin.yml":  draftDAG,
	})
	draft, err := NewDraft(context.Background(), filepath.Join(dir, "twin.yml"))
	require.NoError(t, err)
	require.NoError(t, draft.Write(Expect{Status: "succeeded"}))

	file, err := LoadFile(context.Background(), filepath.Join(dir, "twin.test.yml"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "twin.yml"), file.DAGPath)
	assert.Empty(t, file.Cases[0].Errors)
}

// NewDraft writes nothing and fails for an argument that is not a DAG file
// that loads, and for a DAG with a test file under either suffix.
func TestNewDraftRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files map[string]string
		arg   string
		want  string
	}{
		{"TestFileExists", map[string]string{"release.yaml": draftDAG, "release.test.yaml": ""}, "release.yaml", "release.test.yaml already exists"},
		{"OtherSuffixExists", map[string]string{"release.yaml": draftDAG, "release.test.yml": ""}, "release.yaml", "release.test.yml already exists"},
		{"TestFile", map[string]string{"release.yaml": draftDAG, "release.test.yaml": ""}, "release.test.yaml", "is a test file; give the DAG file it tests"},
		{"NotYAML", map[string]string{"release.json": draftDAG}, "release.json", "is not a DAG file; its name must end in .yaml or .yml"},
		{"Directory", map[string]string{"release.yaml/x.yaml": draftDAG}, "release.yaml", "is a directory, not a DAG file"},
		{"LoadFails", map[string]string{"release.yaml": "steps:\n  - retries: 1\n"}, "release.yaml", "the DAG fails to load"},
		{"BuildDAG", map[string]string{"compile.yaml": `
type: build
steps:
  - id: compile
    outputs:
      - name: artifact
        path: artifact.bin
    run: compiler -o "${outputs.artifact}"
`}, "compile.yaml", "type: build"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := writeFiles(t, tt.files)
			before := listFiles(t, dir)
			_, err := NewDraft(context.Background(), filepath.Join(dir, tt.arg))
			require.ErrorContains(t, err, tt.want)
			assert.Equal(t, before, listFiles(t, dir))
		})
	}
}

// Write never replaces a file, also one created after the draft.
func TestDraftWriteKeepsFile(t *testing.T) {
	t.Parallel()

	dir := writeFiles(t, map[string]string{"release.yaml": draftDAG})
	draft, err := NewDraft(context.Background(), filepath.Join(dir, "release.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(draft.File.Path, []byte("kept"), 0o600))

	require.ErrorIs(t, draft.Write(Expect{Status: "succeeded"}), fs.ErrExist)
	data, err := os.ReadFile(draft.File.Path)
	require.NoError(t, err)
	assert.Equal(t, "kept", string(data))
}

// writeFiles writes each file under a new directory, by its slash-separated
// path, and returns the directory.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	return dir
}

// listFiles returns the paths of the files under dir.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()

	var files []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			files = append(files, path)
		}
		return err
	}))
	return files
}
