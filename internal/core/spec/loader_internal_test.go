// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExpandHomeDir verifies the loader expands only the current user's home shorthand.
func TestExpandHomeDir(t *testing.T) {
	t.Parallel()

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	expanded, err := expandHomeDir("~")
	require.NoError(t, err)
	assert.Equal(t, homeDir, expanded)

	expanded, err = expandHomeDir("~/dags/test.yaml")
	require.NoError(t, err)
	assert.Equal(
		t,
		filepath.Clean(filepath.Join(homeDir, "dags", "test.yaml")),
		filepath.Clean(expanded),
	)

	expanded, err = expandHomeDir("~alice/dags/test.yaml")
	require.NoError(t, err)
	assert.Equal(t, "~alice/dags/test.yaml", expanded)
}

// TestUnmarshalData verifies manifest decoding handles empty and malformed YAML inputs.
func TestUnmarshalData(t *testing.T) {
	t.Parallel()

	t.Run("EmptyDocument", func(t *testing.T) {
		t.Parallel()

		data, err := unmarshalData(nil)
		require.NoError(t, err)
		assert.Nil(t, data)
	})

	t.Run("RejectsMalformedYAML", func(t *testing.T) {
		t.Parallel()

		_, err := unmarshalData([]byte("steps: ["))
		require.Error(t, err)
	})
}

// TestDecode verifies manifest decoding preserves raw fields and surfaces validation errors.
func TestDecode(t *testing.T) {
	t.Parallel()

	t.Run("RejectsLabelsAndTagsTogether", func(t *testing.T) {
		t.Parallel()

		_, err := decode(map[string]any{
			"labels": map[string]any{"team": "core"},
			"tags":   []any{"legacy"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "labels and deprecated tags cannot both be set")
	})

	t.Run("CapturesRawHandlerOnAndDefaults", func(t *testing.T) {
		t.Parallel()

		manifest, err := decode(map[string]any{
			"steps": []any{
				map[string]any{
					"name":    "step-1",
					"command": "echo hello",
				},
			},
			"handler_on": map[string]any{
				"failure": map[string]any{
					"command": "echo fail",
				},
			},
			"defaults": map[string]any{
				"shell": "bash",
			},
		})
		require.NoError(t, err)
		require.NotNil(t, manifest)
		require.Contains(t, manifest.handlerOnRaw, "failure")
		require.Equal(t, "echo fail", manifest.handlerOnRaw["failure"]["command"])
		require.Equal(t, "bash", manifest.defaultsRaw["shell"])
	})

	t.Run("ReportsUnknownKeys", func(t *testing.T) {
		t.Parallel()

		_, err := decode(map[string]any{
			"steps": []any{
				map[string]any{
					"name":    "step-1",
					"command": "echo hello",
				},
			},
			"unknown_key": true,
		})
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrNameOrPathRequired))
		assert.Contains(t, err.Error(), "unknown_key")
	})

	t.Run("SuggestsSnakeCaseForWebhookForwardHeaders", func(t *testing.T) {
		t.Parallel()

		_, err := decode(map[string]any{
			"steps": []any{
				map[string]any{
					"name":    "step-1",
					"command": "echo hello",
				},
			},
			"webhook": map[string]any{
				"forwardHeaders": []any{"X-GitHub-Event"},
			},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "forwardHeaders -> webhook.forward_headers")
	})
}

// TestNewManifestDecoderSharesInstance verifies callers reuse the shared decoder instance.
func TestNewManifestDecoderSharesInstance(t *testing.T) {
	t.Parallel()

	first := newManifestDecoder()
	second := newManifestDecoder()

	require.Same(t, first, second)
}
