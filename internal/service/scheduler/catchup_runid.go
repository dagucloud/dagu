// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// catchupPrefix is the scheme identifier for catchup run IDs.
	catchupPrefix = "catchup-"
	// oneOffPrefix is the scheme identifier for one-off scheduled run IDs.
	oneOffPrefix = "oneoff-"
	// restartPrefix is the scheme identifier for restart run IDs.
	restartPrefix = "restart-"

	// maxRunIDLen matches exec.maxDAGRunIDLen (64 chars).
	maxRunIDLen = 64

	// hashLen is the number of hex characters from the SHA-256 hash.
	hashLen       = 12
	legacyHashLen = 8

	// timestampLayout is the format for the scheduled time portion.
	timestampLayout = "20060102T150405"
)

// GenerateCatchupRunID produces a deterministic run ID for a catchup run.
func GenerateCatchupRunID(dagName string, scheduledTime time.Time) string {
	return generateScheduledRunID(catchupPrefix, dagName, scheduledTime)
}

// GenerateOneOffRunID produces a deterministic run ID for a one-off schedule.
func GenerateOneOffRunID(dagName, fingerprint string, scheduledTime time.Time) string {
	return generateScheduledRunID(oneOffPrefix, dagName+":"+fingerprint, scheduledTime)
}

// GenerateRestartRunID produces a deterministic run ID for a restart. It is
// derived from the run the restart follows, so evaluating the same restart twice,
// for example around a failover, names the same run. previousRunID is empty for
// the first run of a DAG. previousEnd is when the previous run ended; it is zero
// when there is no previous run or its end is unknown, and the ID then has no
// time part.
func GenerateRestartRunID(dagName, previousRunID string, previousEnd time.Time) string {
	if previousEnd.IsZero() {
		return restartPrefix + scheduledRunIDHash(dagName+":"+previousRunID, hashLen)
	}
	return generateScheduledRunID(restartPrefix, dagName+":"+previousRunID, previousEnd)
}

func generateScheduledRunID(prefix, hashSource string, scheduledTime time.Time) string {
	hash := scheduledRunIDHash(hashSource, hashLen)
	ts := scheduledTime.UTC().Format(timestampLayout)
	return fmt.Sprintf("%s%s-%s", prefix, hash, ts)
}

func generateLegacyCatchupRunID(dagName string, scheduledTime time.Time) string {
	return generateLegacyScheduledRunID(catchupPrefix, dagName, dagName, scheduledTime)
}

func generateLegacyOneOffRunID(dagName, fingerprint string, scheduledTime time.Time) string {
	return generateLegacyScheduledRunID(oneOffPrefix, dagName, dagName+":"+fingerprint, scheduledTime)
}

func generateLegacyScheduledRunID(prefix, dagName, hashSource string, scheduledTime time.Time) string {
	sanitized := sanitizeDagName(dagName)
	ts := scheduledTime.UTC().Format(timestampLayout)
	maxNameLen := maxRunIDLen - len(prefix) - 1 - legacyHashLen - 1 - len(timestampLayout)
	if len(sanitized) > maxNameLen {
		sanitized = sanitized[:maxNameLen]
	}
	return fmt.Sprintf("%s%s-%s-%s", prefix, sanitized, scheduledRunIDHash(hashSource, legacyHashLen), ts)
}

func sanitizeDagName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

func scheduledRunIDHash(source string, length int) string {
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:])[:length]
}
