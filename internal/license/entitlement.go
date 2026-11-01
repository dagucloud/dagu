// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package license

import "time"

// HasActiveLicense reports whether a license that unlocks paid features is
// loaded and currently valid or still inside its grace period. The free plan
// unlocks none. It intentionally does not check a feature claim so
// already-issued licenses can unlock newly grouped team capabilities without
// requiring license re-issuance.
func HasActiveLicense(checker Checker) bool {
	claims := usableClaims(checker)
	return claims != nil && !claims.IsFree()
}

// HasLicense reports whether any license, including a free one, is loaded and
// currently valid or still inside its grace period.
func HasLicense(checker Checker) bool {
	return usableClaims(checker) != nil
}

// usableClaims returns the claims of the loaded license while it is valid or
// inside its grace period, and nil otherwise.
func usableClaims(checker Checker) *LicenseClaims {
	if checker == nil || checker.IsCommunity() {
		return nil
	}
	claims := checker.Claims()
	if claims == nil {
		return nil
	}
	if claims.ExpiresAt == nil || claims.ExpiresAt.After(time.Now()) || checker.IsGracePeriod() {
		return claims
	}
	return nil
}
