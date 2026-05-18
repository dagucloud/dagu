// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import "time"

// HasActiveLicense returns true for any loaded license that is currently valid
// or still inside its grace period. It intentionally does not check a feature
// claim so already-issued licenses can unlock newly grouped team capabilities
// without requiring license re-issuance.
func HasActiveLicense(checker Checker) bool {
	if checker == nil || checker.IsCommunity() {
		return false
	}
	claims := checker.Claims()
	if claims == nil {
		return false
	}
	if claims.ExpiresAt == nil {
		return true
	}
	return claims.ExpiresAt.After(time.Now()) || checker.IsGracePeriod()
}
