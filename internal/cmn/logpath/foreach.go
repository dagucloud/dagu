// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logpath

import (
	"fmt"
	"os"
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

	// ForeachKeysFile marks a foreach directory whose step directories are
	// named by fileutil.SafeKey. A run of this version writes it.
	ForeachKeysFile = ".safekey"

	// foreachItemPathSeparator joins the segments of an item path. SafeKey
	// never produces it, so a path splits unambiguously.
	foreachItemPathSeparator = "."
)

var foreachSafeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_%-]+$`)

// ForeachStepDir returns the directory holding the item bodies of a foreach
// step whose log files are in logDir. With siblings empty, it returns the
// directory a step of this version writes to, and the step marks its parent
// with ForeachKeysFile. A reader passes in siblings the names of all steps
// whose log files share logDir. A directory under an unmarked parent was
// named by an earlier version with SafeName, which other steps could share;
// it is returned only when no other step did, and otherwise the result is
// empty.
func ForeachStepDir(logDir, stepName string, siblings []string) string {
	dir := filepath.Join(logDir, ForeachLogDirName)
	if _, err := os.Lstat(filepath.Join(dir, ForeachKeysFile)); len(siblings) == 0 || err == nil {
		return filepath.Join(dir, fileutil.SafeKey(stepName))
	}
	legacy, ok := fileutil.LegacySafeName(stepName, siblings)
	if !ok {
		return ""
	}
	rel, ok := fileutil.LegacyEntry(dir, legacy)
	if !ok {
		return ""
	}
	return filepath.Join(dir, rel)
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
// step running inside item itemPath, whose directory is itemDir. siblings are
// the item's body steps. The path names the step's directory as it is
// stored, and is empty when ForeachStepDir finds no directory to read.
func ForeachNestedParent(itemDir, itemPath, bodyStepName string, siblings []string) string {
	dir := ForeachStepDir(itemDir, bodyStepName, siblings)
	if dir == "" {
		return ""
	}
	return itemPath + foreachItemPathSeparator + filepath.Base(dir)
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
