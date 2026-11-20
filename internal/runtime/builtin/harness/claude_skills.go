// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/skill"
)

// Spec 091, "Skills": the directory of the mounted skills that an attempt with
// the provider claude builds around the process.

// skillsSubdir is the folder, under the agent directory, where the CLI looks
// for skills.
var skillsSubdir = filepath.Join(".claude", "skills")

// materializeSkills writes each skill as <agent directory>/.claude/skills/<name>/
// in a new directory that only the owner can read, and returns the agent
// directory and the function that deletes it. Dagu mounts the whole folder of
// the skill, verbatim: SKILL.md with its original bytes, and the files of
// references/, assets/ and scripts/. Dagu never runs a script and does not
// give a file the execute permission. It returns an empty directory when there
// is no skill.
func materializeSkills(skills []ir.SkillEntry) (string, func(), error) {
	if len(skills) == 0 {
		return "", func() {}, nil
	}
	dir, err := os.MkdirTemp("", "dagu-agent-dir-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to create the agent directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	for _, entry := range skills {
		folder := filepath.Join(dir, skillsSubdir, entry.Name)
		if err := mountSkill(folder, entry); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}

// mountSkill writes the files of one skill into folder.
func mountSkill(folder string, entry ir.SkillEntry) error {
	if !skill.IsSafeFolderName(entry.Name) {
		return fmt.Errorf("the skill name %q cannot be a folder name", entry.Name)
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return fmt.Errorf("failed to create the folder of the skill %q: %w", entry.Name, err)
	}
	if len(entry.Files) == 0 {
		return fmt.Errorf("skill %q: the record of the run has no file of the skill", entry.Name)
	}
	for _, f := range entry.Files {
		rel, err := skill.CheckPath(f.Path)
		if err != nil {
			return fmt.Errorf("skill %q: file %q: %w", entry.Name, f.Path, err)
		}
		data, err := skillFileContent(entry, f)
		if err != nil {
			return err
		}
		target := filepath.Join(folder, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("failed to create the folder of %s of the skill %q: %w", rel, entry.Name, err)
		}
		if err := writeMounted(target, data); err != nil {
			return fmt.Errorf("failed to write %s of the skill %q: %w", rel, entry.Name, err)
		}
	}
	return nil
}

func writeMounted(path string, data []byte) error {
	return os.WriteFile(path, data, 0o400)
}

// skillFileContent returns the bytes of one file of a skill. A text file comes
// from the record of the run. A binary file comes from the skill folder, and
// Dagu checks it against the SHA-256 of the record: a folder that changed
// after the run got its version fails the step, and never gives other bytes.
func skillFileContent(entry ir.SkillEntry, f ir.SkillFile) ([]byte, error) {
	var data []byte
	if f.Text != nil {
		data = []byte(*f.Text)
	} else {
		if entry.Dir == "" {
			return nil, fmt.Errorf("skill %q: the file %s is not in the record of the run and the skill has no folder", entry.Name, f.Path)
		}
		read, err := os.ReadFile(filepath.Join(entry.Dir, filepath.FromSlash(f.Path))) //nolint:gosec // The path passed skill.CheckPath.
		if err != nil {
			return nil, fmt.Errorf("skill %q: file %s: %w (the skill changed after the run got version %s)", entry.Name, f.Path, err, entry.Hash)
		}
		data = read
	}
	if f.SHA256 != "" {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return nil, fmt.Errorf("skill %q: file %s changed after the run got version %s of the skill", entry.Name, f.Path, entry.Hash)
		}
	}
	return data, nil
}
