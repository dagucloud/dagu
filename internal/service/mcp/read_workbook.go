// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
)

// workbookSampleRows is how many typed rows per sheet the workbook target
// returns, enough for an agent to write a correct xlsx.read.
const workbookSampleRows = 5

// readWorkbook describes a workbook on the server's filesystem. The path is
// any file the server process can read, the same trust DAG authoring has,
// and it is recorded in the audit log.
func readWorkbook(ctx context.Context, input readInput) (any, error) {
	path, err := fileutil.ResolvePath(input.Path)
	if err != nil {
		return nil, invalidTargetValue(input.Target, readFieldPath, err.Error())
	}
	if path, err = filepath.Abs(path); err != nil {
		return nil, invalidTargetValue(input.Target, readFieldPath, err.Error())
	}
	info, err := workbook.Inspect(ctx, path, workbook.InspectOptions{SampleRows: workbookSampleRows})
	if err != nil {
		return nil, classifyWorkbookError(input, err)
	}
	return info, nil
}

func classifyWorkbookError(input readInput, err error) *readToolError {
	var locked *workbook.LockedError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return resourceNotFoundReadError(input, err.Error())
	case errors.Is(err, workbook.ErrUnsupportedFormat):
		return invalidTargetValue(input.Target, readFieldPath, err.Error())
	case errors.As(err, &locked), errors.Is(err, workbook.ErrNotWorkbook):
		return &readToolError{Code: readErrorResourceUnavailable, Message: err.Error(), Target: input.Target}
	default:
		// A canceled or timed-out read is resource_unavailable like every
		// other target; anything else is internal.
		return classifyReadToolError(input, err)
	}
}
