// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// startBrowserReaper closes browsers that browser steps left open for input
// once no step can resume them. It stops when ctx is cancelled.
func startBrowserReaper(ctx context.Context, dataDir string, repository *persis.DAGRunRepository) {
	if dataDir == "" {
		return
	}
	store := browserhost.NewStore(filepath.Join(dataDir, browserhost.DataDirName))
	go browserhost.RunReaper(ctx, store, browserStepResumable(repository))
}

// browserStepResumable keeps a browser while its step has not finished. A run
// whose status this process cannot read keeps its browser until the ask
// deadline.
func browserStepResumable(repository *persis.DAGRunRepository) browserhost.ResumableFunc {
	if repository == nil {
		return nil
	}
	return func(ctx context.Context, record browserhost.Record) bool {
		attempt, err := repository.FindAttempt(ctx, ir.NewDAGRunRef(record.DAGName, record.DAGRunID))
		if err != nil {
			return true
		}
		status, err := attempt.ReadStatus(ctx)
		if err != nil || status == nil {
			return true
		}
		for _, node := range status.Nodes {
			if node != nil && node.Step.Name == record.StepName {
				return !node.Status.IsDone()
			}
		}
		return true
	}
}
