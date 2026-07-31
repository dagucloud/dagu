// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildArtifactPreviewReturnsMetadataWithoutContentForLargeMarkdownFiles(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	content := strings.Repeat("# heading\n", int(artifactTextPreviewMaxBytes/8)+1)
	path := filepath.Join(archiveDir, "large.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	preview, err := buildArtifactPreview(archiveDir, "large.md")
	require.NoError(t, err)
	assert.Equal(t, openapiv1.ArtifactPreviewKindMarkdown, preview.Kind)
	assert.True(t, preview.TooLarge)
	assert.False(t, preview.Truncated)
	assert.Nil(t, preview.Content)
	assert.Greater(t, preview.Size, artifactTextPreviewMaxBytes)
}

func TestBuildArtifactPreviewReturnsMetadataWithoutContentForLargeImages(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	content := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, bytes.Repeat([]byte("a"), int(artifactImagePreviewMaxBytes))...)
	path := filepath.Join(archiveDir, "large.png")
	require.NoError(t, os.WriteFile(path, content, 0o600))

	preview, err := buildArtifactPreview(archiveDir, "large.png")
	require.NoError(t, err)
	assert.Equal(t, openapiv1.ArtifactPreviewKindImage, preview.Kind)
	assert.True(t, preview.TooLarge)
	assert.Nil(t, preview.Content)
	assert.Greater(t, preview.Size, artifactImagePreviewMaxBytes)
}

func TestBuildArtifactPreviewAllowsLargerTextWithinTextLimit(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	content := strings.Repeat("line\n", 300000)
	path := filepath.Join(archiveDir, "report.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	preview, err := buildArtifactPreview(archiveDir, "report.txt")
	require.NoError(t, err)
	assert.Equal(t, openapiv1.ArtifactPreviewKindText, preview.Kind)
	assert.False(t, preview.TooLarge)
	require.NotNil(t, preview.Content)
	assert.NotEmpty(t, *preview.Content)
}

func TestResolveArtifactPathRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("secret"), 0o600))

	linkPath := filepath.Join(archiveDir, "escape.txt")
	require.NoError(t, os.Symlink(outsidePath, linkPath))

	_, err := resolveArtifactPath(archiveDir, "escape.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestListArtifactTreeSkipsSymlinks(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("outside"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "inside.txt"), []byte("inside"), 0o600))
	require.NoError(t, os.Symlink(outsidePath, filepath.Join(archiveDir, "link.txt")))

	items, err := listArtifactTree(archiveDir, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "inside.txt", items[0].Name)
}

func TestListArtifactTreeShallowLeavesDirectoryChildrenCollapsed(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	nestedDir := filepath.Join(archiveDir, "reports")
	require.NoError(t, os.MkdirAll(nestedDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(nestedDir, "summary.txt"), []byte("ok"), 0o600))

	items, err := listArtifactTree(archiveDir, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, openapiv1.ArtifactNodeTypeDirectory, items[0].Type)
	assert.Nil(t, items[0].Children)
}

func TestListArtifactTreeSortsCaseOnlyNameDifferencesDeterministically(t *testing.T) {
	t.Parallel()

	items := []openapiv1.ArtifactTreeNode{
		{Name: "alpha.txt", Type: openapiv1.ArtifactNodeTypeFile},
		{Name: "Alpha.txt", Type: openapiv1.ArtifactNodeTypeFile},
	}

	sortArtifactTreeNodes(items)
	assert.Equal(t, []string{"Alpha.txt", "alpha.txt"}, []string{items[0].Name, items[1].Name})
}

func TestArtifactPreviewAndDownloadRejectNamedPipes(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("named pipes via mkfifo are not supported on Windows")
	}

	archiveDir := t.TempDir()
	pipePath := filepath.Join(archiveDir, "artifact.pipe")
	require.NoError(t, makeNamedPipe(pipePath))

	_, previewErr := buildArtifactPreview(archiveDir, "artifact.pipe")
	require.ErrorIs(t, previewErr, os.ErrNotExist)

	file, info, downloadErr := openArtifactFile(archiveDir, "artifact.pipe")
	require.ErrorIs(t, downloadErr, os.ErrNotExist)
	assert.Nil(t, file)
	assert.Nil(t, info)
}
