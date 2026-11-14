// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// dryRunCheck reports a file the step reads that is not on this host.
// A path that still holds a reference is skipped, and so is a problem the
// dry run cannot judge. file.write and file.mkdir create their path, so a
// missing destination is not a problem. copy and move with dry_run set do
// not look at the source.
func dryRunCheck(ctx context.Context, step ir.Step) error {
	cfg := defaultConfig()
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil
	}
	workDir := runtime.GetEnv(ctx).WorkingDir
	switch stepOperation(step) {
	case opRead, opList:
		return missingFile(workDir, cfg.Path, "path", true)
	case opStat:
		if cfg.MissingOK {
			return nil
		}
		return missingFile(workDir, cfg.Path, "path", cfg.FollowSymlinks)
	case opDelete:
		if cfg.MissingOK {
			return nil
		}
		return missingFile(workDir, cfg.Path, "path", false)
	case opCopy:
		if cfg.DryRun {
			return nil
		}
		return missingFile(workDir, cfg.Source, "source", cfg.FollowSymlinks)
	case opMove:
		if cfg.DryRun {
			return nil
		}
		return missingFile(workDir, cfg.Source, "source", false)
	default:
		return nil
	}
}

func missingFile(workDir, raw, field string, follow bool) error {
	// Params and environment are already resolved, so a "$" left in the path
	// is a reference only the run can resolve, such as a step output variable.
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "$") {
		return nil
	}
	path, err := resolveFilePath(workDir, raw)
	if err != nil {
		return nil
	}
	var statErr error
	if follow {
		_, statErr = os.Stat(path)
	} else {
		_, statErr = os.Lstat(path)
	}
	if statErr == nil || !pathAbsent(statErr) {
		return nil
	}
	return fmt.Errorf("field 'with.%s': %s: file not found", field, filepath.Base(path))
}

// pathAbsent reports that the path is not present. ENOTDIR means a parent is
// a file, so the path cannot exist. On Windows that case is ERROR_DIRECTORY
// (267); syscall.ENOTDIR is ERROR_PATH_NOT_FOUND and does not match it.
// Permission and other errors are left unreported; a dry run cannot judge them.
func pathAbsent(err error) bool {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return true
	}
	return goruntime.GOOS == "windows" && errors.Is(err, syscall.Errno(267))
}
