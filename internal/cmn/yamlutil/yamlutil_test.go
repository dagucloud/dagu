// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package yamlutil_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/yamlutil"
	"github.com/goccy/go-yaml/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClearEmptyDocumentSeparators(t *testing.T) {
	t.Run("NoEmptyDocument", func(t *testing.T) {
		data := []byte("a: 1\n---\nb: 2\n")
		assert.Equal(t, data, yamlutil.ClearEmptyDocumentSeparators(data))
	})

	t.Run("EmptyDocumentBetweenDocs", func(t *testing.T) {
		requireDocBodies(t, "a: 1\n---\n---\nb: 2\n", "a: 1", "b: 2")
	})

	t.Run("EmptyDocumentWithComment", func(t *testing.T) {
		requireDocBodies(t, "a: 1\n--- # nothing here\n---\nb: 2\n", "a: 1", "b: 2")
	})

	t.Run("LeadingEmptyDocuments", func(t *testing.T) {
		requireDocBodies(t, "---\n---\na: 1\n", "a: 1")
	})

	t.Run("MarkerInsideLiteralBlockUntouched", func(t *testing.T) {
		data := []byte("a: |\n  ---\n  text\n---\nb: 2\n")
		got := yamlutil.ClearEmptyDocumentSeparators(data)
		assert.Equal(t, data, got)
		file, err := parser.ParseBytes(got, 0)
		require.NoError(t, err)
		require.Len(t, file.Docs, 2)
	})

	t.Run("LineCountPreserved", func(t *testing.T) {
		data := []byte("a: 1\n---\n---\nb: 2\n")
		got := yamlutil.ClearEmptyDocumentSeparators(data)
		assert.Equal(t,
			strings.Count(string(data), "\n"),
			strings.Count(string(got), "\n"))
	})
}

// requireDocBodies asserts that the cleared stream parses into exactly the
// given document bodies, so no real document is lost behind an empty one.
func requireDocBodies(t *testing.T, data string, want ...string) {
	t.Helper()
	file, err := parser.ParseBytes(yamlutil.ClearEmptyDocumentSeparators([]byte(data)), 0)
	require.NoError(t, err)
	got := make([]string, 0, len(file.Docs))
	for _, doc := range file.Docs {
		require.NotNil(t, doc.Body)
		got = append(got, doc.Body.String())
	}
	assert.Equal(t, want, got)
}
