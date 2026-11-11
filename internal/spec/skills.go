// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/skill"
	"github.com/goccy/go-yaml"
)

const (
	// SkillsDirName is the name of the directory, next to the DAG files, that
	// holds the skill folders.
	SkillsDirName = "skills"
	// SkillFileName is the name of the file that defines a skill. It is in the
	// folder of the skill.
	SkillFileName = skill.FileName
)

// skillResolver returns the checked skill that the name gives.
type skillResolver func(name string) (*skill.Skill, error)

// newSkillResolver reads skill folders from the skills directory that the
// search of the DAG being built finds. The result of a name is cached for the
// build of one DAG only.
func newSkillResolver(ctx buildContext) skillResolver {
	cache := make(map[string]*skill.Skill)
	return func(name string) (*skill.Skill, error) {
		if sk, ok := cache[name]; ok {
			return sk, nil
		}
		sk, err := loadSkill(ctx, name)
		if err != nil {
			return nil, err
		}
		cache[name] = sk
		return sk, nil
	}
}

// loadSkill reads the folder <skills directory>/<name>. A skill that is not in
// the format of Spec 096 is refused. The error names the skill, the file and
// the rule, and the folders that Dagu searched.
func loadSkill(ctx buildContext, name string) (*skill.Skill, error) {
	if !skill.IsSafeFolderName(name) {
		return nil, fmt.Errorf("skill name %q is not a valid skill name: %s", name, skill.NamePattern)
	}
	dirs, found := sideDirs(ctx, SkillsDirName)
	if found == "" {
		return nil, fmt.Errorf("skill %q: no folder %s/%s; Dagu searched %s",
			name, name, SkillFileName, searchedList(dirs))
	}
	sk, err := skill.Read(filepath.Join(found, name))
	if err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			return nil, fmt.Errorf("skill %q: no folder %s; Dagu searched %s",
				name, filepath.Join(found, name), searchedList(dirs))
		}
		return nil, fmt.Errorf("skill %q: %w", name, err)
	}
	if err := sk.Err(); err != nil {
		return nil, fmt.Errorf("%w (folder %s; Dagu searched %s)", err, sk.Dir, searchedList(dirs))
	}
	for _, f := range sk.Files {
		ctx.sideFiles.add(filepath.Join(sk.Dir, filepath.FromSlash(f.Path)))
	}
	warnings := make([]string, 0, len(sk.Warnings()))
	for _, w := range sk.Warnings() {
		warnings = append(warnings, fmt.Sprintf("skill %q: %s", name, w.String()))
	}
	ctx.sideFiles.addSkill(ir.SkillRef{Name: name, Dir: sk.Dir, Hash: sk.Hash}, warnings)
	return sk, nil
}

// skillEntryList holds the raw items of a skills list. The loader parses them
// once the build context is known.
type skillEntryList []any

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (l *skillEntryList) UnmarshalYAML(data []byte) error {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("skills unmarshal error: %w", err)
	}
	items, ok := raw.([]any)
	if !ok {
		return skillsListError(raw)
	}
	*l = items
	return nil
}

// skillsListError is the error of a value of the key skills that is not a list.
func skillsListError(raw any) error {
	return fmt.Errorf("skills must be a list of skill names, got %s", describeYAMLKind(raw))
}

// parseSkillEntries reads the items of a skills list and loads each skill. An
// item MUST be a skill name. A name that occurs more than once is listed once:
// the first entry wins. The loader counts the names before it reads a folder.
func parseSkillEntries(ctx buildContext, items []any, path string) ([]ir.SkillEntry, error) {
	var names, paths []string
	for i, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		name, err := parseSkillName(item, itemPath)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
			paths = append(paths, itemPath)
		}
		if len(names) > skill.MaxSkillsPerStep {
			return nil, ir.NewValidationError(path, len(items), skillCountError(len(items)))
		}
	}
	resolve := newSkillResolver(ctx)
	var entries []ir.SkillEntry
	for i, name := range names {
		sk, err := resolve(name)
		if err != nil {
			return nil, ir.NewValidationError(paths[i], name, err)
		}
		entries = append(entries, NewSkillEntry(sk))
	}
	if err := checkSkillCaps(entries); err != nil {
		return nil, ir.NewValidationError(path, len(entries), err)
	}
	return entries, nil
}

// skillCountError is the error of a list with more skills than a step can use.
func skillCountError(n int) error {
	return fmt.Errorf("a step can use %d skills at most, but the list has %d", skill.MaxSkillsPerStep, n)
}

// parseSkillName reads the written form of one item: a skill name. It does not
// load the skill.
func parseSkillName(item any, path string) (string, error) {
	name, ok := item.(string)
	if !ok {
		return "", ir.NewValidationError(path, item,
			fmt.Errorf("a skill item must be a skill name (a string), got %s", describeYAMLKind(item)))
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ir.NewValidationError(path, item, errors.New("a skill name must not be empty"))
	}
	return name, nil
}

// NewSkillEntry makes the entry of a checked skill: the snapshot that a run
// keeps. The text files hold their content.
func NewSkillEntry(sk *skill.Skill) ir.SkillEntry {
	entry := ir.SkillEntry{
		Name:        sk.Name,
		Description: sk.Meta.Description,
		Body:        sk.Body,
		Hash:        sk.Hash,
		Dir:         sk.Dir,
		Files:       make([]ir.SkillFile, 0, len(sk.Files)),
	}
	for _, f := range sk.Files {
		file := ir.SkillFile{Path: f.Path, Size: f.Size, SHA256: f.SHA256}
		if data, ok := sk.Text(f.Path); ok {
			text := string(data)
			file.Text = &text
		}
		entry.Files = append(entry.Files, file)
	}
	return entry
}

// checkSkillCaps applies the limits of the skills of one step: the number of
// skills and the size of the catalog.
func checkSkillCaps(entries []ir.SkillEntry) error {
	if len(entries) > skill.MaxSkillsPerStep {
		return skillCountError(len(entries))
	}
	if tokens := CatalogTokens(entries); tokens > skill.CatalogTokenBudget {
		return fmt.Errorf("the catalog of the skills is about %d tokens, above the budget of %d tokens; shorten the descriptions or use fewer skills",
			tokens, skill.CatalogTokenBudget)
	}
	return nil
}

// CatalogTokens estimates the tokens of the catalog lines of the skills.
func CatalogTokens(entries []ir.SkillEntry) int {
	n := 0
	for _, e := range entries {
		n += len("- ") + len(e.Name) + len(": ") + len(e.Description) + 1
	}
	return skill.EstimateTokens(n)
}
