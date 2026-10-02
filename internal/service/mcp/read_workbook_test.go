// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateWorkbookReadInput(t *testing.T) {
	t.Parallel()
	ok := readInput{Target: readTargetWorkbook, Path: "orders.xlsx"}
	require.Nil(t, validateTargetReadInput(&ok))
	assert.Empty(t, ok.URI, "the workbook target has no resource URI")

	for _, tc := range []struct {
		name  string
		input readInput
		field string
	}{
		{"missing path", readInput{Target: readTargetWorkbook}, readFieldPath},
		{"wrong extension", readInput{Target: readTargetWorkbook, Path: "book.xls"}, readFieldPath},
		{"name forbidden", readInput{Target: readTargetWorkbook, Path: "a.xlsx", Name: "x"}, readFieldName},
		{"query forbidden", readInput{Target: readTargetWorkbook, Path: "a.xlsx", Query: "x"}, readFieldQuery},
		{"workspace forbidden", readInput{Target: readTargetWorkbook, Path: "a.xlsx", Workspace: "default"}, readFieldWorkspace},
		{"path forbidden on dags", readInput{Target: readTargetDAGs, Path: "a.xlsx"}, readFieldPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := tc.input
			err := validateTargetReadInput(&input)
			require.NotNil(t, err)
			assert.Equal(t, readErrorInvalidToolInput, err.Code)
			assert.Equal(t, tc.field, err.Field)
		})
	}
}

func TestReadWorkbookErrors(t *testing.T) {
	t.Parallel()
	input := readInput{Target: readTargetWorkbook, Path: filepath.Join(t.TempDir(), "missing.xlsx")}
	_, err := readWorkbook(context.Background(), input)
	var readErr *readToolError
	require.True(t, errors.As(err, &readErr))
	assert.Equal(t, readErrorResourceNotFound, readErr.Code)

	assert.Equal(t, readErrorResourceUnavailable, classifyWorkbookError(input, &workbook.LockedError{Path: "a.xlsx"}).Code)
	assert.Equal(t, readErrorResourceUnavailable, classifyWorkbookError(input, workbook.ErrNotWorkbook).Code)
	assert.Equal(t, readErrorInvalidToolInput, classifyWorkbookError(input, workbook.ErrUnsupportedFormat).Code)
	assert.Equal(t, readErrorInternal, classifyWorkbookError(input, errors.New("boom")).Code)
}

func TestReadAuditMetadataWorkbookPath(t *testing.T) {
	t.Parallel()
	meta := readAuditMetadata(readInput{Target: readTargetWorkbook, Path: "/data/orders.xlsx"})
	assert.Equal(t, "/data/orders.xlsx", meta.Attributes["workbook_path"])
	assert.NotContains(t, meta.Attributes, "doc_path")
	assert.Equal(t, "/data/orders.xlsx", meta.ResourceID)

	wiki := readAuditMetadata(readInput{Target: readTargetWikiPage, Workspace: "default", Path: "guides/intro"})
	assert.Equal(t, "guides/intro", wiki.Attributes["doc_path"])
	assert.NotContains(t, wiki.Attributes, "workbook_path")
}
