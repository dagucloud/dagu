// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	// ForeachLogDirName is the directory, next to a foreach step's own log
	// files, that holds the log files of its item bodies.
	ForeachLogDirName = "foreach"

	// ForeachItemsFile lists the expanded items of a foreach step. It lives
	// in the step's foreach directory.
	ForeachItemsFile = "items.json"

	// ForeachItemStatusFile records an item's body run. It lives in the
	// item's directory.
	ForeachItemStatusFile = "status.json"

	// foreachItemPathSeparator joins the segments of an item path. SafeName
	// never produces it, so a path splits unambiguously.
	foreachItemPathSeparator = "."
)

var foreachSafeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ForeachStepDir returns the directory holding the item bodies of a foreach
// step whose own log file is stepLogFile.
func ForeachStepDir(stepLogFile, stepName string) string {
	return filepath.Join(filepath.Dir(stepLogFile), ForeachLogDirName, fileutil.SafeName(stepName))
}

// ForeachItemPath builds the item path of an item nested under parent. An
// empty parent addresses a top-level item.
func ForeachItemPath(parent string, index int) string {
	if parent == "" {
		return strconv.Itoa(index)
	}
	return parent + foreachItemPathSeparator + strconv.Itoa(index)
}

// ForeachNestedParent builds the parent path for the items of a foreach body
// step running inside item itemPath.
func ForeachNestedParent(itemPath, bodyStepName string) string {
	return itemPath + foreachItemPathSeparator + fileutil.SafeName(bodyStepName)
}

// ForeachItemDir resolves an item path to its directory under stepDir. A path
// is "<index>" for a top-level item and "<index>.<bodyStep>.<index>" for an
// item of a nested foreach, where <bodyStep> is the safe name of the body
// step. Every segment is validated so the result stays inside stepDir.
func ForeachItemDir(stepDir, itemPath string) (string, error) {
	segments, endsWithIndex, err := splitForeachItemPath(itemPath)
	if err != nil {
		return "", err
	}
	if !endsWithIndex {
		return "", fmt.Errorf("invalid foreach item path %q: must end with an item index", itemPath)
	}
	return filepath.Join(stepDir, filepath.Join(segments...)), nil
}

// ForeachParentDir resolves a parent path to the foreach directory of the
// nested step it names. An empty parent resolves to stepDir itself.
func ForeachParentDir(stepDir, parent string) (string, error) {
	if parent == "" {
		return stepDir, nil
	}
	segments, endsWithIndex, err := splitForeachItemPath(parent)
	if err != nil {
		return "", err
	}
	if endsWithIndex {
		return "", fmt.Errorf("invalid foreach parent path %q: must end with a body step", parent)
	}
	return filepath.Join(stepDir, filepath.Join(segments...)), nil
}

// splitForeachItemPath validates a path and returns its directory segments,
// each index as is and each body step as "foreach/<safe name>", along with
// whether the path ends with an index.
func splitForeachItemPath(path string) ([]string, bool, error) {
	if path == "" {
		return nil, false, fmt.Errorf("foreach item path is empty")
	}
	parts := strings.Split(path, foreachItemPathSeparator)
	segments := make([]string, 0, len(parts)+len(parts)/2)
	for i, part := range parts {
		if i%2 == 0 {
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || strconv.Itoa(index) != part {
				return nil, false, fmt.Errorf("invalid foreach item path %q: %q is not an item index", path, part)
			}
			segments = append(segments, part)
			continue
		}
		if !foreachSafeNamePattern.MatchString(part) {
			return nil, false, fmt.Errorf("invalid foreach item path %q: %q is not a body step name", path, part)
		}
		segments = append(segments, ForeachLogDirName, part)
	}
	return segments, len(parts)%2 == 1, nil
}
