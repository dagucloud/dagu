// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"os"
)

// Spec 092, "The restricted user". The block untrusted has a closed schema. An
// unknown key, a value of the wrong type, a value out of the range, one key
// without the other, or isolation: off with a restricted user stops Dagu at
// start. The error names the key, the expected range and the value. The block is
// file only: no environment variable sets it or a key of it.

// Limits of untrusted.uid and untrusted.gid.
const (
	UntrustedIDMin = 1
	UntrustedIDMax = 4294967294
)

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

// Values of the key untrusted.isolation.
const (
	UntrustedIsolationOn  = "on"
	UntrustedIsolationOff = "off"
)

// UntrustedKeyDocs lists each key of the block with its type and its default,
// for the reference and for the printed configuration.
var UntrustedKeyDocs = []UntrustedKeyDoc{
	{"isolation", "string", UntrustedIsolationOn, "on or off. With on, an untrusted step needs untrusted.uid and untrusted.gid, and fails without them. With off, an untrusted step runs without isolation, as the process user. Off is for development and tests. Not valid with untrusted.uid and untrusted.gid."},
	{"uid", "integer", "none", "The user ID of the restricted user. Required with untrusted.gid. From 1 to 4294967294. Not the user ID of the process."},
	{"gid", "integer", "none", "The group ID of the restricted user. Required with untrusted.uid. From 1 to 4294967294. Not the group ID of the process."},
}

// UntrustedKeyDoc documents one key of the block untrusted.
type UntrustedKeyDoc struct {
	Key         string
	Type        string
	Default     string
	Description string
}

// ParseUntrustedConfig checks the raw block and returns the typed
// configuration. raw is the value of the key untrusted, or nil when the file has
// none. uid and gid are the real and effective IDs of the process, which the
// restricted user must not have.
func ParseUntrustedConfig(raw any, processUIDs, processGIDs []int) (UntrustedConfig, error) {
	if raw == nil {
		return UntrustedConfig{}, nil
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return UntrustedConfig{}, fmt.Errorf("config: key %q: expected an object with the keys isolation, uid and gid, got %s",
			"untrusted", showConfigValue(raw))
	}
	for _, key := range sortedMapKeys(block) {
		if key != "uid" && key != "gid" && key != "isolation" {
			return UntrustedConfig{}, fmt.Errorf("config: unknown key %q in the block untrusted; valid keys are gid, isolation, uid",
				"untrusted."+key)
		}
	}
	off := false
	if rawIsolation, has := block["isolation"]; has {
		switch rawIsolation {
		case UntrustedIsolationOn:
		case UntrustedIsolationOff:
			off = true
		default:
			return UntrustedConfig{}, fmt.Errorf("config: key %q: expected %q or %q, got %s",
				"untrusted.isolation", UntrustedIsolationOn, UntrustedIsolationOff, showConfigValue(rawIsolation))
		}
	}
	rawUID, hasUID := block["uid"]
	rawGID, hasGID := block["gid"]
	switch {
	case !hasUID && !hasGID:
		return UntrustedConfig{IsolationOff: off}, nil
	case !hasUID:
		return UntrustedConfig{}, fmt.Errorf("config: key %q is required with %q", "untrusted.uid", "untrusted.gid")
	case !hasGID:
		return UntrustedConfig{}, fmt.Errorf("config: key %q is required with %q", "untrusted.gid", "untrusted.uid")
	case off:
		return UntrustedConfig{}, fmt.Errorf("config: key %q is %q and the keys %q and %q are set: isolation cannot be off with a restricted user",
			"untrusted.isolation", UntrustedIsolationOff, "untrusted.uid", "untrusted.gid")
	}
	uid, err := untrustedID("untrusted.uid", rawUID, processUIDs, "user")
	if err != nil {
		return UntrustedConfig{}, err
	}
	gid, err := untrustedID("untrusted.gid", rawGID, processGIDs, "group")
	if err != nil {
		return UntrustedConfig{}, err
	}
	return UntrustedConfig{Enabled: true, UID: uid, GID: gid}, nil
}

// untrustedID checks one ID. It accepts an integer of the YAML file only: a
// string or a number with a decimal point is an error.
func untrustedID(key string, value any, processIDs []int, kind string) (uint32, error) {
	id, valid := configInt(value)
	if !valid || id < UntrustedIDMin || id > UntrustedIDMax {
		return 0, fmt.Errorf("config: key %q: expected an integer from %d to %d, got %s",
			key, int64(UntrustedIDMin), int64(UntrustedIDMax), showConfigValue(value))
	}
	for _, processID := range processIDs {
		if processID >= 0 && int64(processID) == id {
			return 0, fmt.Errorf("config: key %q is set to %d, which is the %s ID of the process: the restricted user needs its own ID",
				key, id, kind)
		}
	}
	return uint32(id), nil
}

// loadUntrustedConfig reads the block untrusted of the file.
func (l *ConfigLoader) loadUntrustedConfig(cfg *Config) error {
	parsed, err := ParseUntrustedConfig(l.v.Get("untrusted"),
		[]int{os.Getuid(), os.Geteuid()}, []int{os.Getgid(), os.Getegid()})
	if err != nil {
		return err
	}
	cfg.Untrusted = parsed
	if parsed.IsolationOff {
		l.warnings = append(l.warnings, `untrusted.isolation is "off": an untrusted step runs without isolation, as the process user; use it for development only`)
	}
	return nil
}
