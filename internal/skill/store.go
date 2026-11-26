// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
