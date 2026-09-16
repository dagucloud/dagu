// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

var (
	// ErrInvalidArtifactCursor reports a cursor that does not belong to the
	// query it was presented with.
	ErrInvalidArtifactCursor = errors.New("invalid artifact cursor")

	// ErrInvalidArtifactFileName reports a file name pattern that is not a
	// valid glob.
	ErrInvalidArtifactFileName = errors.New("invalid artifact file name pattern")
)

// ArtifactStore lists root DAG runs that produced artifacts, newest first.
type ArtifactStore interface {
	QueryArtifacts(ctx context.Context, query ArtifactQuery) (ArtifactPage, error)
}

// ArtifactQuery selects a page of runs.
type ArtifactQuery struct {
	// Name matches DAG names containing it, case-insensitively.
	Name string

	// FileName selects which of a run's files are returned. A pattern holding
	// glob metacharacters is a glob; anything else is a case-insensitive
	// substring. A run with no matching file is omitted.
	FileName string

	// From and To bound CreatedAt, not StartedAt.
	From TimeInUTC
	To   TimeInUTC

	WorkspaceFilter *workspace.WorkspaceFilter

	Limit  int
	Cursor string
}

// ArtifactPage is one forward-only page of runs.
type ArtifactPage struct {
	Items      []ArtifactRun
	NextCursor string
}

// ArtifactRun is one DAG run together with the files it produced.
type ArtifactRun struct {
	Name     string
	DAGRunID string

	// CreatedAt is when the run's artifact directory was made, which is the
	// value the date range filters and the listing orders on.
	CreatedAt time.Time

	// StartedAt is when the run began, which for a run that waited in a queue
	// is later than CreatedAt.
	StartedAt time.Time

	// Files are in walk order, and only those matching the query's FileName
	// when one is set.
	Files []ArtifactFile

	// FilesTruncated reports that the run holds more files than were returned.
	FilesTruncated bool
}

// ArtifactFile is one file within a run's artifact directory.
type ArtifactFile struct {
	// Path is relative to the run's artifact directory and uses forward
	// slashes on every platform.
	Path string
	Size int64
}

// globMetacharacters are the characters that make a file name pattern a glob
// rather than a substring.
const globMetacharacters = "*?[{"

// IsArtifactFileNameGlob reports whether a file name pattern is a glob.
func IsArtifactFileNameGlob(pattern string) bool {
	return strings.ContainsAny(pattern, globMetacharacters)
}

// MatchArtifactFileName reports whether a run-relative artifact path matches a
// file name pattern. A pattern holding glob metacharacters is matched as a
// glob, the way artifact.list matches its own pattern; anything else is matched
// as a case-insensitive substring.
func MatchArtifactFileName(path, pattern string) bool {
	if pattern == "" {
		return true
	}
	if IsArtifactFileNameGlob(pattern) {
		ok, err := doublestar.Match(pattern, path)
		return err == nil && ok
	}
	return strings.Contains(strings.ToLower(path), strings.ToLower(pattern))
}
