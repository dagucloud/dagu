// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// InvalidError is returned when a path, or the content of a write, breaks a
// rule.
type InvalidError struct{ Issues []Issue }

func (e *InvalidError) Error() string {
	parts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		parts[i] = issue.String()
	}
	return strings.Join(parts, "; ")
}

// Store lists the skill folders of one skills directory. A Store holds no
// state of the skills: every call reads the disk. Several Stores, in several
// processes, can use the same directory.
type Store struct {
	root string
}

// NewStore returns the store of the skills directory root.
func NewStore(root string) *Store { return &Store{root: root} }

// Dir returns the folder of a skill.
func (s *Store) Dir(name string) string { return filepath.Join(s.root, name) }

// Read reads one skill. It returns ErrNotFound when the folder does not exist
// or the name cannot be the name of a folder.
func (s *Store) Read(name string) (*Skill, error) {
	if !IsSafeFolderName(name) {
		return nil, ErrNotFound
	}
	return Read(s.Dir(name))
}

// MaxListedSkills is the number of folders that one listing reads at most.
const MaxListedSkills = 1000

// Names returns the names of the first MaxListedSkills folders of the skills
// directory, sorted, and whether the directory holds more. A folder is listed
// even when it has no SKILL.md or breaks the format: it is a skill with errors.
// Hidden folders and plain files are not listed.
func (s *Store) Names() (names []string, more bool, err error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || !IsSafeFolderName(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) > MaxListedSkills {
		return names[:MaxListedSkills], true, nil
	}
	return names, false, nil
}

// isCleanRelative reports whether rel is a relative path with / separators and
// no empty, . or .. part.
func isCleanRelative(rel string) bool {
	if strings.ContainsRune(rel, '\\') || path.IsAbs(rel) {
		return false
	}
	for p := range strings.SplitSeq(rel, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

// CheckExistingPath checks a path of an entry of a skill folder that exists
// already, for a read or a delete. It does not apply the layout: a skill that
// breaks the layout has entries that the owner must be able to remove. It
// rejects every path that leaves the folder.
func CheckExistingPath(rel string) (string, error) {
	if rel == "" || len(rel) > MaxPathLength || !utf8.ValidString(rel) || !isCleanRelative(rel) {
		return "", &InvalidError{Issues: []Issue{pathIssue(rel, "the path must be relative, with / separators, and without . or .. parts")}}
	}
	for p := range strings.SplitSeq(rel, "/") {
		if msg := checkPathSegment(p); msg != "" {
			return "", &InvalidError{Issues: []Issue{pathIssue(rel, "%s", msg)}}
		}
	}
	return rel, nil
}

func pathIssue(rel, format string, args ...any) Issue {
	return Issue{Severity: SeverityError, Rule: RulePath, File: rel, Message: fmt.Sprintf(format, args...)}
}
