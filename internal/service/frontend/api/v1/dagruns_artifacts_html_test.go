// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	frontendapi "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildArtifactPreviewClassifiesHTMLFiles(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "report.html", body: "<!doctype html><html><body><h1>Report</h1></body></html>"},
		{name: "fragment.htm", body: "<section>Fragment</section>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, os.WriteFile(filepath.Join(archiveDir, tc.name), []byte(tc.body), 0o600))

			preview, err := frontendapi.BuildArtifactPreviewForTest(archiveDir, tc.name)
			require.NoError(t, err)
			assert.Equal(t, openapiv1.ArtifactPreviewKindHtml, preview.Kind)
			assert.Equal(t, "text/html", preview.MimeType)
			require.NotNil(t, preview.Content)
			assert.Equal(t, tc.body, *preview.Content)
			assert.False(t, preview.TooLarge)
			assert.False(t, preview.Truncated)
		})
	}
}

func TestBuildArtifactPreviewReturnsMetadataWithoutContentForLargeHTMLFiles(t *testing.T) {
	t.Parallel()

	archiveDir := t.TempDir()
	body := strings.Repeat("<p>x</p>", int(frontendapi.ArtifactTextPreviewMaxBytesForTest/8)+1)
	require.NoError(t, os.WriteFile(filepath.Join(archiveDir, "large.html"), []byte(body), 0o600))

	preview, err := frontendapi.BuildArtifactPreviewForTest(archiveDir, "large.html")
	require.NoError(t, err)
	assert.Equal(t, openapiv1.ArtifactPreviewKindHtml, preview.Kind)
	assert.Equal(t, "text/html", preview.MimeType)
	assert.True(t, preview.TooLarge)
	assert.False(t, preview.Truncated)
	assert.Nil(t, preview.Content)
	assert.Greater(t, preview.Size, frontendapi.ArtifactTextPreviewMaxBytesForTest)
}
