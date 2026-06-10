// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import "context"

// UpdateLatestAttemptPointerForTest exposes latest-attempt pointer updates to external tests.
func UpdateLatestAttemptPointerForTest(ctx context.Context, statusFile string) error {
	return updateLatestAttemptPointer(ctx, statusFile)
}

// LatestAttemptPointerPathForTest exposes latest-attempt pointer paths to external tests.
func LatestAttemptPointerPathForTest(dagRunsDir string) string {
	return latestAttemptPointerPath(dagRunsDir)
}
