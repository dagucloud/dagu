// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/stretchr/testify/require"
)

// A file whose content changes during the stream aborts the response, so no
// error body joins the partial file.
func TestSkillFileStreamAborts(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "demo")
	file := filepath.Join(dir, "assets", "a.bin")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\n---\n"), 0o600))
	require.NoError(t, os.WriteFile(file, []byte("abcd"), 0o600))
	sk, err := skill.Read(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, []byte("abce"), 0o600))
	body, err := sk.Open("assets/a.bin")
	require.NoError(t, err)

	response := &skillFileResponse{ctx: t.Context(), body: body, size: 4, filename: "a.bin", etag: `"` + sk.Hash + `"`}
	w := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { _ = response.VisitGetSkillFileResponse(w) })
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Body.Bytes())
}

// A file that changes during the stream of a ZIP aborts the response, so no
// error body joins the partial ZIP and the client cannot read it as a whole.
func TestSkillZipStreamAborts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	file := filepath.Join(dir, "assets", "a.bin")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: demo\n---\n"), 0o600))
	require.NoError(t, os.WriteFile(file, []byte("abcd"), 0o600))
	skills, err := skill.NewStore(root).DownloadSkills([]string{"demo"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, []byte("abce"), 0o600))

	response := &skillZipResponse{ctx: t.Context(), skills: skills, disposition: `attachment; filename="demo.zip"`}
	w := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { _ = response.VisitDownloadSkillResponse(w) })
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), `"code"`)
	_, err = zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.Error(t, err)
}
