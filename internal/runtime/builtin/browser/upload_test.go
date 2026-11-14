// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFiles creates files with the given names under dir and returns their
// paths.
func writeFiles(t *testing.T, dir string, names ...string) []string {
	t.Helper()
	paths := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(name), 0o600))
		paths = append(paths, path)
	}
	return paths
}

// uploadSteps is a step that opens the receipts page and uploads files with
// the given upload spec.
func uploadSteps(t *testing.T, upload map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"url": "https://books.example.com/receipts", "do": []any{map[string]any{"upload": upload}}})
	require.NoError(t, err)
	return string(data)
}

// uploadPage is the receipts page as the accessibility tree shows it: a
// button, and a drop area with no name.
func uploadPage(button string) pageSnapshot {
	return pageSnapshot{
		Tree:   "[0-1] RootWebArea: Receipts\n  [0-5] button: " + button + "\n  [0-6] generic",
		XPaths: map[string]string{"0-1": "/", "0-5": "/html/body/button", "0-6": "/html/body/div"},
	}
}

// Without into, the page's only file input gets the files and no model is
// asked.
func TestUploadIntoThePageFileInput(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf", "receipt.png")
	run := newTestRun(t, pageModel(nil))
	execution := run.execute(uploadSteps(t, map[string]any{"files": files}), nil)
	require.NoError(t, execution.err)

	assert.Equal(t, []fakeUpload{{selector: "", paths: files}}, run.engine.uploaded())
	assert.Zero(t, run.provider.callCount())
	assert.Equal(t, []string{"goto:completed", "upload:completed"}, eventNames(execution.exec.GetAgentSession()))
	assert.Contains(t, execution.stderr.String(), "invoice.pdf, receipt.png into the page's file input")
}

// The element into describes is found by the model once; later runs replay
// it without the model while the page shows the same element, and ask the
// model again when it does not.
func TestUploadRecordsTheElementTheModelFinds(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	steps := uploadSteps(t, map[string]any{"files": files[0], "into": "the Upload receipt button"})
	run := newTestRun(t, pageModel(nil))
	run.engine.snapshot = uploadPage("Upload receipt")
	run.engine.uploadHow = uploadPicker

	first := run.execute(steps, nil)
	require.NoError(t, first.err)
	assert.Equal(t, 1, run.provider.callCount())
	assert.Equal(t, []string{"goto:completed", "upload:completed"}, eventNames(first.exec.GetAgentSession()))
	assert.Contains(t, first.stderr.String(), "invoice.pdf through the file picker xpath=/html/body/button opened")
	request := lastUserText(run.provider.calls[0])
	assert.Contains(t, request, "the Upload receipt button")
	assert.NotContains(t, request, "invoice.pdf", "the model never sees the files")

	second := run.execute(steps, nil)
	require.NoError(t, second.err)
	assert.Equal(t, 1, run.provider.callCount(), "a replay makes no model call")
	assert.Equal(t, []string{"goto:completed", "upload:cache-hit"}, eventNames(second.exec.GetAgentSession()))

	run.engine.snapshot = uploadPage("Delete receipt")
	healed := run.execute(steps, nil)
	require.NoError(t, healed.err)
	assert.Equal(t, 2, run.provider.callCount())
	assert.Equal(t, []string{"goto:completed", "upload:healed"}, eventNames(healed.exec.GetAgentSession()))

	for _, upload := range run.engine.uploaded() {
		assert.Equal(t, fakeUpload{selector: "xpath=/html/body/button", paths: files}, upload)
	}
}

func TestUploadCacheCanBeDisabled(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	steps := uploadSteps(t, map[string]any{"files": files, "into": "the Upload receipt button", "cache": false})
	run := newTestRun(t, pageModel(nil))
	require.NoError(t, run.execute(steps, nil).err)
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, 2, run.provider.callCount())
}

// Under ai: never, an upload into a recorded element runs with no model
// configured, and one whose element is no longer on the page fails the step
// without asking for it.
func TestUploadUnderNeverReplaysWithoutModel(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	run := newTestRun(t, pageModel(nil))
	run.engine.snapshot = uploadPage("Upload receipt")
	require.NoError(t, run.execute(uploadSteps(t, map[string]any{"files": files[0], "into": "the Upload receipt button"}), nil).err)

	run.noModel = true
	steps := uploadSteps(t, map[string]any{"files": files[0], "into": "the Upload receipt button", "ai": "never"})
	replayed := run.execute(steps, nil)
	require.NoError(t, replayed.err)
	assert.Equal(t, 1, run.provider.callCount(), "a replay makes no model call")
	assert.Equal(t, []string{"goto:", "upload:element"}, operationVias(replayed.exec.GetAgentSession()))

	run.engine.snapshot = uploadPage("Delete receipt")
	missed := run.execute(steps, nil)
	require.ErrorContains(t, missed.err, "do[0] upload failed: the upload cannot run without AI: "+
		"its recorded element xpath=/html/body/button is gone or no longer the one recorded")
	assert.Equal(t, 1, run.provider.callCount())
	assert.Len(t, run.engine.uploaded(), 2, "the missed upload gives no file")
}

// An upload that fails on the page fails the step with the reason, and the
// recording it replayed is dropped.
func TestUploadFailureFailsTheStep(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "a.pdf", "b.pdf")
	steps := uploadSteps(t, map[string]any{"files": files, "into": "the Upload receipt button"})
	run := newTestRun(t, pageModel(nil))
	require.NoError(t, run.execute(steps, nil).err)

	run.engine.uploadErr = errSingleFile{files: 2}
	failed := run.execute(steps, nil)
	require.ErrorContains(t, failed.err, "do[0] upload failed: the file input takes one file, and the upload gives 2")

	run.engine.uploadErr = nil
	again := run.execute(steps, nil)
	require.NoError(t, again.err)
	assert.Equal(t, []string{"goto:completed", "upload:completed"}, eventNames(again.exec.GetAgentSession()))
}

func TestUploadOfMissingFileFailsBeforeThePage(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.pdf")
	run := newTestRun(t, pageModel(nil))
	execution := run.execute(uploadSteps(t, map[string]any{"files": missing, "into": "the Upload receipt button"}), nil)
	require.ErrorContains(t, execution.err, "file "+missing+" does not exist")
	assert.Empty(t, run.engine.uploaded())
	assert.Zero(t, run.provider.callCount())
}

func TestUploadWithNoMatchingElementNamesTheModel(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	run := newTestRun(t, func(*llmpkg.ChatRequest) (string, error) { return `{"elements":[]}`, nil })
	execution := run.execute(uploadSteps(t, map[string]any{"files": files, "into": "the receipt box"}), nil)
	require.ErrorContains(t, execution.err, `the model (test-model) answered that no element on the page matches "the receipt box"`)
	assert.Empty(t, run.engine.uploaded())
}

func TestUploadIntoHoldingSecretIsRejected(t *testing.T) {
	t.Parallel()

	files := writeFiles(t, t.TempDir(), "invoice.pdf")
	run := newTestRun(t, pageModel(nil))
	run.secrets = map[string]string{"ACCOUNT": "acct-7731"}
	execution := run.execute(uploadSteps(t, map[string]any{"files": files, "into": "the receipt box of acct-7731"}), nil)
	require.ErrorContains(t, execution.err, "do[0].upload contains the value of secret ACCOUNT")
	assert.Zero(t, run.provider.callCount())
}

func TestResolveUploadFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := writeFiles(t, dir, "invoices/b.pdf", "invoices/a.pdf", "invoices/notes.txt", "receipt.png")
	b, a, notes, receipt := paths[0], paths[1], paths[2], paths[3]
	attachments, err := json.Marshal([]map[string]any{
		{"name": "a.pdf", "content_type": "application/pdf", "path": a},
		{"name": "receipt.png", "content_type": "image/png", "path": receipt},
	})
	require.NoError(t, err)
	attachment, err := json.Marshal(map[string]any{"name": "b.pdf", "path": b})
	require.NoError(t, err)
	emails, err := json.Marshal([]map[string]any{
		{"subject": "Invoice", "attachments": []map[string]any{{"name": "b.pdf", "path": b}}},
		{"subject": "Thanks", "attachments": nil},
		{"subject": "Receipt", "attachments": []map[string]any{{"name": "receipt.png", "path": receipt}}},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		entries []string
		want    []string
	}{
		{name: "relative to the working directory", entries: []string{"receipt.png"}, want: []string{receipt}},
		{name: "absolute", entries: []string{receipt}, want: []string{receipt}},
		{name: "glob, sorted", entries: []string{"invoices/*.pdf"}, want: []string{a, b}},
		{name: "one per line", entries: []string{receipt + "\n\n" + notes + "\n"}, want: []string{receipt, notes}},
		{name: "JSON list of paths", entries: []string{`["receipt.png","invoices/b.pdf"]`}, want: []string{receipt, b}},
		{name: "JSON list of attachments", entries: []string{string(attachments)}, want: []string{a, receipt}},
		{name: "JSON attachment", entries: []string{string(attachment)}, want: []string{b}},
		{name: "emails with saved attachments", entries: []string{string(emails)}, want: []string{b, receipt}},
		{name: "each file once", entries: []string{"invoices/*.pdf", a}, want: []string{a, b}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := resolveUploadFiles(tc.entries, dir)
			require.NoError(t, err)
			got := make([]string, 0, len(files))
			for _, file := range files {
				got = append(got, file.Path)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveUploadFilesRefuses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFiles(t, dir, "invoices/a.pdf")
	large := filepath.Join(dir, "scan.tiff")
	file, err := os.Create(large)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxUploadFileBytes+1))
	require.NoError(t, file.Close())

	for _, tc := range []struct {
		name  string
		entry string
		want  string
	}{
		{name: "missing file", entry: "missing.pdf", want: "file " + filepath.Join(dir, "missing.pdf") + " does not exist"},
		{name: "pattern matching nothing", entry: "invoices/*.xlsx", want: "no file matches " + filepath.Join(dir, "invoices", "*.xlsx")},
		{name: "directory", entry: "invoices", want: "is a directory; name the files in it, such as " + filepath.Join(dir, "invoices", "*.pdf")},
		{name: "unsaved attachment", entry: `[{"name":"a.pdf","size":10}]`, want: `files lists "a.pdf" without a path; save mail attachments with save_attachments: true`},
		{name: "email with an unsaved attachment", entry: `[{"subject":"Invoice","attachments":[{"name":"a.pdf"}]}]`, want: `files lists "a.pdf" without a path`},
		{name: "listed number", entry: `[1]`, want: "files lists 1, which is neither a path nor an object with a path"},
		{name: "too large", entry: "scan.tiff", want: "is 51 MB; an upload takes files of up to 50 MB"},
		{name: "nothing", entry: "[]", want: "files names no file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveUploadFiles([]string{tc.entry}, dir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestResolveUploadFilesFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	paths := writeFiles(t, home, "Downloads/invoice.pdf")

	files, err := resolveUploadFiles([]string{"~/Downloads/*.pdf"}, t.TempDir())
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, paths[0], files[0].Path)
	assert.Equal(t, int64(len("Downloads/invoice.pdf")), files[0].Size)
}

func TestUploadMIMEType(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		"invoice.pdf":   "application/pdf",
		"Summary.XLSX":  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"rows.csv":      "text/csv",
		"notes.txt":     "text/plain",
		"photo.jpeg":    "image/jpeg",
		"archive.zz9z9": "",
	} {
		assert.Equal(t, want, uploadMIMEType(path), path)
	}
	assert.False(t, strings.Contains(uploadMIMEType("page.html"), ";"), "a type carries no parameters")
}
