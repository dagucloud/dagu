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

// Store reads and writes the skill folders of one skills directory. A Store
// holds no state of the skills: every call reads the disk. Several Stores,
// in several processes, can use the same directory.
type Store struct {
	root string
}

// NewStore returns the store of the skills directory root.
func NewStore(root string) *Store { return &Store{root: root} }

// Dir returns the folder of a skill.
func (s *Store) Dir(name string) string { return filepath.Join(s.root, name) }

// Names returns the names of the folders of the skills directory, sorted. A
// folder is listed even when it has no SKILL.md or breaks the format: it is a
// skill with errors. Hidden folders and plain files are not listed.
func (s *Store) Names() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || !IsSafeFolderName(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// List reads every skill of the directory, sorted by name.
func (s *Store) List() ([]*Skill, error) {
	names, err := s.Names()
	if err != nil {
		return nil, err
	}
	out := make([]*Skill, 0, len(names))
	for _, name := range names {
		sk, err := Read(s.Dir(name))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, nil
}
