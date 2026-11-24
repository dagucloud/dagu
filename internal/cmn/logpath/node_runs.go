// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	// NodeRunsDirName is the directory, next to a step's own log files, that
	// holds the records of the node runs of steps with nodes.
	NodeRunsDirName = "node-runs"

	// NodeRunsFile records the run of each node a step matched. It lives in
	// the step's node runs directory.
	NodeRunsFile = "runs.json"

	// NodeRunsSummaryFile counts the step's node runs by status. It lives
	// beside NodeRunsFile.
	NodeRunsSummaryFile = "summary.json"
)

// NodeRunsDir returns the directory holding the node run records of a step
// with nodes whose log files are in logDir.
func NodeRunsDir(logDir, stepName string) string {
	return filepath.Join(logDir, NodeRunsDirName, fileutil.SafeKey(stepName))
}
