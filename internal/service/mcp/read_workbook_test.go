// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

func TestValidateWorkbookReadInput(t *testing.T) {
	t.Parallel()
	ok := readInput{Target: readTargetWorkbook, Path: "orders.xlsx", Password: "secret"}
	require.Nil(t, validateTargetReadInput(&ok))
	assert.Empty(t, ok.URI, "the workbook target has no resource URI")
	assert.Equal(t, "secret", ok.Password)

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
		{"password forbidden on dags", readInput{Target: readTargetDAGs, Password: "secret"}, readFieldPassword},
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

func TestParseWorkbookPasswordKeepsSpaces(t *testing.T) {
	t.Parallel()
	input, err := parseReadToolInput(json.RawMessage(`{"target":"workbook","path":" orders.xlsx ","password":" secret "}`))
	require.Nil(t, err)
	assert.Equal(t, "orders.xlsx", input.Path)
	assert.Equal(t, " secret ", input.Password)

	spaces, err := parseReadToolInput(json.RawMessage(`{"target":"workbook","path":"a.xlsx","password":"   "}`))
	require.Nil(t, err)
	assert.Equal(t, "   ", spaces.Password)

	empty, err := parseReadToolInput(json.RawMessage(`{"target":"workbook","path":"a.xlsx","password":""}`))
	require.Nil(t, err)
	assert.Empty(t, empty.Password)

	_, err = parseReadToolInput(json.RawMessage(`{"target":"dags","password":"   "}`))
	require.NotNil(t, err)
	assert.Equal(t, readFieldPassword, err.Field)
}

func TestReadWorkbookErrors(t *testing.T) {
	t.Parallel()
	input := readInput{Target: readTargetWorkbook, Path: filepath.Join(t.TempDir(), "missing.xlsx")}
	_, err := readWorkbook(context.Background(), input)
	var readErr *readToolError
	require.True(t, errors.As(err, &readErr))
	assert.Equal(t, readErrorResourceNotFound, readErr.Code)

	protected := filepath.Join(t.TempDir(), "protected.xlsx")
	f := excelize.NewFile()
	require.NoError(t, f.SaveAs(protected, excelize.Options{Password: "secret"}))
	require.NoError(t, f.Close())
	_, err = readWorkbook(context.Background(), readInput{Target: readTargetWorkbook, Path: protected, Password: "nope"})
	require.True(t, errors.As(err, &readErr))
	assert.Equal(t, readFieldPassword, readErr.Field)
	assert.NotContains(t, readErr.Message, "nope", "the supplied password is not echoed")

	assert.Equal(t, readErrorResourceUnavailable, classifyWorkbookError(input, &workbook.LockedError{Path: "a.xlsx"}).Code)
	assert.Equal(t, readErrorResourceUnavailable, classifyWorkbookError(input, workbook.ErrNotWorkbook).Code)
	assert.Equal(t, readErrorResourceUnavailable, classifyWorkbookError(input, workbook.ErrUnsupportedEncryption).Code)
	passwordErr := classifyWorkbookError(input, fmt.Errorf("orders.xlsx: %w", workbook.ErrPassword))
	assert.Equal(t, readErrorInvalidToolInput, passwordErr.Code)
	assert.Equal(t, readFieldPassword, passwordErr.Field)
	assert.Equal(t, "orders.xlsx: workbook password is missing or incorrect", passwordErr.Message)
	assert.Equal(t, readErrorInvalidToolInput, classifyWorkbookError(input, workbook.ErrUnsupportedFormat).Code)
	assert.Equal(t, readErrorInternal, classifyWorkbookError(input, errors.New("boom")).Code)
}

func TestReadAuditMetadataWorkbookPath(t *testing.T) {
	t.Parallel()
	meta := readAuditMetadata(readInput{Target: readTargetWorkbook, Path: "/data/orders.xlsx", Password: "pw-not-logged"})
	assert.Equal(t, "/data/orders.xlsx", meta.Attributes["workbook_path"])
	assert.NotContains(t, meta.Attributes, "doc_path")
	assert.NotContains(t, meta.Attributes, "password")
	assert.Equal(t, "/data/orders.xlsx", meta.ResourceID)
	for _, value := range meta.Attributes {
		assert.NotContains(t, value, "pw-not-logged")
	}

	wiki := readAuditMetadata(readInput{Target: readTargetWikiPage, Workspace: "default", Path: "guides/intro"})
	assert.Equal(t, "guides/intro", wiki.Attributes["doc_path"])
	assert.NotContains(t, wiki.Attributes, "workbook_path")
}
