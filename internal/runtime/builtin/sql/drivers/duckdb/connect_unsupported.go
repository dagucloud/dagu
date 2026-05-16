// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !cgo || !((darwin && (amd64 || arm64)) || (linux && (amd64 || arm64)) || (windows && amd64))

package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"

	sqlexec "github.com/dagucloud/dagu/internal/runtime/builtin/sql"
)

// Connect reports that DuckDB is unavailable for this build target.
func (d *DuckDBDriver) Connect(_ context.Context, _ *sqlexec.Config) (*sql.DB, func() error, error) {
	return nil, nil, fmt.Errorf("duckdb is not supported on %s/%s with cgo disabled or unavailable", runtime.GOOS, runtime.GOARCH)
}
