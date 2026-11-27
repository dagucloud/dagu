// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

// UntrustedConfig is the block untrusted after the checks.
type UntrustedConfig struct {
	// Enabled is true when the file sets untrusted.uid and untrusted.gid. An
	// untrusted step then runs as the restricted user.
	Enabled bool
	// UID and GID are the user ID and the group ID of the restricted user.
	UID uint32
	GID uint32
	// IsolationOff is true when the file sets untrusted.isolation to off. An
	// untrusted step then runs without isolation, as the process user. Without
	// it, and without a restricted user, an untrusted step fails.
	IsolationOff bool
}
