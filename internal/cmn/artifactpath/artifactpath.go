// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package artifactpath builds and parses the on-disk layout of DAG-run
// artifact directories.
//
// Artifacts are partitioned by run date so that a date-range listing costs one
// directory read per day. A run's files live in a directory whose name carries
// the time of day and the DAG name; identity beyond that is recorded in a
// sidecar file next to it, keeping the run ID out of the path so that deeply
// nested artifact paths stay within platform path length limits.
package artifactpath

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
)

// MetaSuffix is appended to a run directory name to address its sidecar.
const MetaSuffix = ".meta"

// SuffixLen is the length of the hex suffix in a run directory name.
//
// It separates two runs of the same DAG started within the same second. At six
// characters a thousand-way parallel fan-out of one DAG collided roughly three
// times in a hundred, and a collision means one run writing into another run's
// directory, so the width is set well past where that matters rather than just
// past where it was observed.
const SuffixLen = 16

const (
	dayLayout       = "2006/01/02"
	timeOfDayLayout = "150405"

	// maxDAGNameLen bounds the DAG name segment. It matches ir.DAGNameMaxLen,
	// duplicated here to keep this package free of domain imports.
	maxDAGNameLen = 40
)

// RunDirName is a parsed per-run artifact directory name.
type RunDirName struct {
	TimeOfDay string
	DAGName   string
	Suffix    string
}

// NewRunDir returns the artifact directory for a run, creating it if needed.
// overrideDir, when set, replaces baseDir as the root; both are expanded for
// environment references before use.
//
// The path is a pure function of its arguments, so any component that has to
// re-derive a run's directory arrives at the same one rather than minting a
// second.
func NewRunDir(ctx context.Context, baseDir, overrideDir, dagName, dagRunID string, at time.Time) (string, error) {
	root, err := resolveRoot(ctx, baseDir, overrideDir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dagName) == "" {
		return "", fmt.Errorf("DAG name must not be empty")
	}
	if strings.TrimSpace(dagRunID) == "" {
		return "", fmt.Errorf("DAG-run ID must not be empty")
	}
	dir := filepath.Join(dayDir(root, at), runDirName(at, dagName, dagRunID))
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("failed to initialize directory %s: %w", dir, err)
	}
	return dir, nil
}

// dayDir returns the directory holding every run started on the given day.
func dayDir(root string, at time.Time) string {
	return filepath.Join(root, filepath.FromSlash(at.UTC().Format(dayLayout)))
}

// MetaPath returns the sidecar path for runDir. The sidecar always lives in the
// global tree under root, even when runDir itself sits under a DAG-level
// override, so that one date walk sees every run.
//
// The day is taken from runDir rather than recomputed, because a run's
// directory is created once and the clock has moved on by the time its status
// is recorded. It reports false for a directory that predates this layout.
func MetaPath(root, runDir string) (string, bool) {
	day, name, ok := splitRunDir(runDir)
	if !ok {
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(day), name+MetaSuffix), true
}

// splitRunDir separates a per-run artifact directory into its "YYYY/MM/DD" day
// and its directory name.
func splitRunDir(runDir string) (day, name string, ok bool) {
	cleaned := filepath.Clean(runDir)
	name = filepath.Base(cleaned)
	if _, ok := ParseRunDirName(name); !ok {
		return "", "", false
	}

	parts := strings.Split(filepath.ToSlash(cleaned), "/")
	if len(parts) < 4 {
		return "", "", false
	}
	year, month, dayOfMonth := parts[len(parts)-4], parts[len(parts)-3], parts[len(parts)-2]
	if len(year) != 4 || !isDigits(year) ||
		len(month) != 2 || !isDigits(month) ||
		len(dayOfMonth) != 2 || !isDigits(dayOfMonth) {
		return "", "", false
	}
	return year + "/" + month + "/" + dayOfMonth, name, true
}

// IsMetaName reports whether name addresses a sidecar file.
func IsMetaName(name string) bool {
	return strings.HasSuffix(name, MetaSuffix)
}

// TrimMetaSuffix returns the run directory name a sidecar name refers to.
func TrimMetaSuffix(name string) string {
	return strings.TrimSuffix(name, MetaSuffix)
}

// ParseRunDirName splits a per-run artifact directory name into its parts. The
// DAG name it reports is the sanitized path segment, which is a filter hint
// only; the sidecar holds the authoritative name.
func ParseRunDirName(name string) (RunDirName, bool) {
	const minLen = len(timeOfDayLayout) + 1 + 1 + 1 + SuffixLen
	if len(name) < minLen {
		return RunDirName{}, false
	}

	timeOfDay := name[:len(timeOfDayLayout)]
	if !isDigits(timeOfDay) || name[len(timeOfDayLayout)] != '_' {
		return RunDirName{}, false
	}

	suffix := name[len(name)-SuffixLen:]
	if !isHex(suffix) || name[len(name)-SuffixLen-1] != '_' {
		return RunDirName{}, false
	}

	dagName := name[len(timeOfDayLayout)+1 : len(name)-SuffixLen-1]
	if dagName == "" {
		return RunDirName{}, false
	}
	return RunDirName{TimeOfDay: timeOfDay, DAGName: dagName, Suffix: suffix}, true
}

func resolveRoot(ctx context.Context, baseDir, overrideDir string) (string, error) {
	resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{})

	// An override that expands to nothing is a misconfiguration rather than a
	// request to fall back to the global root.
	if strings.TrimSpace(overrideDir) != "" {
		expanded, err := resolver.String(ctx, overrideDir, cmnvalue.CoordinatorArtifactBaseDirField("artifacts.dir"))
		if err != nil {
			return "", fmt.Errorf("expand artifact directory: %w", err)
		}
		return trimmedRoot(expanded)
	}

	if strings.TrimSpace(baseDir) == "" {
		return "", fmt.Errorf("artifact directory is not configured")
	}
	expanded, err := resolver.String(ctx, baseDir, cmnvalue.CoordinatorArtifactBaseDirField("artifacts.base_dir"))
	if err != nil {
		return "", fmt.Errorf("expand artifact directory: %w", err)
	}
	return trimmedRoot(expanded)
}

func trimmedRoot(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("artifact directory is empty after expansion")
	}
	return dir, nil
}

func runDirName(at time.Time, dagName, dagRunID string) string {
	return at.UTC().Format(timeOfDayLayout) + "_" + safeDAGName(dagName) + "_" + runSuffix(dagRunID)
}

// runSuffix separates two runs of the same DAG started in the same second. It
// is derived from the run ID rather than drawn at random so that the directory
// can be recomputed, not just remembered. The run ID itself stays out of the
// path to leave room for user-authored artifact paths beneath it.
func runSuffix(dagRunID string) string {
	sum := sha256.Sum256([]byte(dagRunID))
	return hex.EncodeToString(sum[:])[:SuffixLen]
}

// safeDAGName reduces a DAG name to characters that are safe in a path segment
// on every supported platform. Names accepted by DAG validation pass through
// unchanged.
func safeDAGName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-':
			b.WriteRune(r)
		default:
			// '_' separates the name from the surrounding segments, so it is
			// not reproduced here; collapsing to it keeps parsing unambiguous
			// because the boundaries are fixed-width.
			b.WriteRune('_')
		}
	}

	safe := b.String()
	if len(safe) > maxDAGNameLen {
		safe = safe[:maxDAGNameLen]
	}
	if safe == "" {
		safe = "dag"
	}
	return safe
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}
