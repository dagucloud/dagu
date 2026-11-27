// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import "time"

// SetOpenRetryForTest makes c first open failed stores again after wait.
func (c *ProjectStoreCache) SetOpenRetryForTest(wait time.Duration) {
	c.retryMin = wait
}
