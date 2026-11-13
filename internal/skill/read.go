// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrNotFound is returned when the skill folder does not exist.
var ErrNotFound = errors.New("skill not found")

// maxReadAttempts is the number of times that a read starts again when the
// folder changes while Dagu reads it.
const maxReadAttempts = 4

// readRetryPause is the pause before the second attempt of a read. Each later
// attempt waits one pause more.
const readRetryPause = 10 * time.Millisecond

// maxWalkEntries bounds the walk of one folder.
const maxWalkEntries = MaxFiles * 4

// cacheRacyWindow is the age under which a file change is too recent for the
// cache: a later change could keep the same size and the same time stamp.
const cacheRacyWindow = 2 * time.Second

const maxCacheEntries = 128

// tempNamePattern matches the name of the temporary file that an atomic write
// creates next to its target. A reader ignores it.
var tempNamePattern = regexp.MustCompile(`\.tmp\.[0-9]+$`)

type entryKind int

const (
	kindFile entryKind = iota
	kindLink
	kindSpecial
)

// entry is one non-directory item of the walk of a folder.
type entry struct {
	rel   string
	kind  entryKind
	size  int64
	mtime int64
	mode  fs.FileMode
}

var cache = struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
}{entries: map[string]cacheEntry{}}

type cacheEntry struct {
	signature string
	skill     *Skill
}

// Read reads the skill folder dir. The name of the skill is the name of the
// folder. It never fails on a violation of the format: the violations are
// issues of the result. It returns ErrNotFound when the folder does not exist.
//
// Read does not trust a shared state. It reads the folder from disk. It keeps a
// cache that is keyed by the paths, the sizes and the modification times of
// all entries, and it ignores the cache for a change that is too recent. A
// folder that changes while Read reads it is read again. A folder that does not
// settle gives an error with the rule "changing".
//
// The caller MUST NOT change the result.
func Read(dir string) (*Skill, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(abs)
	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		// The hash holds the target of the link, so it is not the hash of an empty
		// skill and it changes with the link.
		target, _ := os.Readlink(abs)
		sum := sha256.Sum256([]byte("symlink:" + target))
		return &Skill{Name: name, Dir: abs, Hash: hashLines([]hashLine{{path: "", sum: hex.EncodeToString(sum[:])}}), Issues: []Issue{{
			Severity: SeverityError, Rule: RuleFolderLink,
			Message: "the skill folder is a symbolic link; a skill folder must be a real directory"}}}, nil
	}
	if !info.IsDir() {
		return nil, ErrNotFound
	}

	// A writer saves the files of a skill one after the other. The attempts wait a
	// little longer each time, so a read that meets a write gets the next version.
	unreadable := false
	for attempt := range maxReadAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * readRetryPause)
		}
		before, folderIssues, newest, err := walk(abs)
		if err != nil {
			if _, statErr := os.Lstat(abs); errors.Is(statErr, fs.ErrNotExist) {
				return nil, ErrNotFound
			}
			unreadable = !errors.Is(err, fs.ErrNotExist)
			continue
		}
		signature := signatureOf(before)
		if cached, ok := lookup(abs, signature, newest); ok {
			return cached, nil
		}
		s, err := build(abs, name, before, folderIssues)
		if err != nil {
			unreadable = !errors.Is(err, fs.ErrNotExist)
			continue
		}
		after, _, _, err := walk(abs)
		if err != nil || signatureOf(after) != signature {
			unreadable = false
			continue
		}
		store(abs, signature, newest, s)
		return s, nil
	}
	// The message names no path of the server.
	if unreadable {
		return &Skill{Name: name, Dir: abs, Hash: emptyHash(), Issues: []Issue{{
			Severity: SeverityError, Rule: RuleUnreadable,
			Message: "a file or a folder of the skill cannot be read; check its permissions"}}}, nil
	}
	return &Skill{Name: name, Dir: abs, Hash: emptyHash(), Issues: []Issue{{
		Severity: SeverityError, Rule: RuleChanging,
		Message: "the skill is being changed and could not be read as one version; try again"}}}, nil
}

func lookup(dir, signature string, newest time.Time) (*Skill, bool) {
	if time.Since(newest) < cacheRacyWindow {
		return nil, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	c, ok := cache.entries[dir]
	if ok && c.signature == signature {
		return c.skill, true
	}
	return nil, false
}

func store(dir, signature string, newest time.Time, s *Skill) {
	if time.Since(newest) < cacheRacyWindow {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) >= maxCacheEntries {
		for k := range cache.entries {
			delete(cache.entries, k)
			break
		}
	}
	cache.entries[dir] = cacheEntry{signature: signature, skill: s}
}

func emptyHash() string { return hashLines(nil) }

func signatureOf(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.rel)
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(int(e.kind)))
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(e.size, 10))
		b.WriteByte('|')
		b.WriteString(strconv.FormatInt(e.mtime, 10))
		b.WriteByte('|')
		b.WriteString(strconv.FormatUint(uint64(e.mode), 8))
		b.WriteByte('\n')
	}
	return b.String()
}

// walk lists the non-directory entries of the folder, sorted by path, and the
// issues of the layout. It reads no file content.
func walk(dir string) (entries []entry, issues []Issue, newest time.Time, err error) { //nolint:revive // the walk gives the entries, the issues, the newest time and an error
	add := func(rule, file, format string, args ...any) {
		issues = append(issues, Issue{Severity: SeverityError, Rule: rule, File: file, Message: fmt.Sprintf(format, args...)})
	}
	count := 0
	var visit func(rel string) error
	visit = func(rel string) error {
		items, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		for _, item := range items {
			name := item.Name()
			path := name
			if rel != "" {
				path = rel + "/" + name
			}
			if count++; count > maxWalkEntries {
				return nil
			}
			if msg := checkPathSegment(name); msg != "" {
				add(RulePath, path, "%s", msg)
				continue
			}
			if len(path) > MaxPathLength {
				add(RulePath, path, "the path is %d bytes long; the limit is %d bytes", len(path), MaxPathLength)
				continue
			}
			info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(path)))
			if err != nil {
				return err
			}
			if mt := info.ModTime(); mt.After(newest) {
				newest = mt
			}
			mode := info.Mode()
			if !mode.IsDir() && tempNamePattern.MatchString(name) {
				continue // the temporary file of an atomic write that is in progress
			}
			if rel == "" { // root of the skill folder
				switch {
				case name == FileName:
					if mode&fs.ModeSymlink != 0 {
						add(RuleSymlink, path, "SKILL.md is a symbolic link; a symbolic link is not allowed")
						entries = append(entries, entry{rel: path, kind: kindLink, mtime: info.ModTime().UnixNano(), mode: mode})
						continue
					}
					if !mode.IsRegular() {
						add(RuleSpecialFile, path, "SKILL.md must be a regular file")
						entries = append(entries, entry{rel: path, kind: kindSpecial, mtime: info.ModTime().UnixNano(), mode: mode})
						continue
					}
					if linkCount(info) > 1 {
						add(RuleHardlink, path, "a hard link is not allowed; the file has %d links", linkCount(info))
					}
					entries = append(entries, entry{rel: path, kind: kindFile, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: mode.Perm()})
					continue
				case (name == DirReferences || name == DirAssets || name == DirScripts) && mode.IsDir():
					if err := visit(path); err != nil {
						return err
					}
					continue
				default:
					add(RuleRootEntry, path, "%q is not allowed at the root of a skill folder; only SKILL.md, references/, assets/ and scripts/ are allowed", name)
					if mode.IsRegular() {
						entries = append(entries, entry{rel: path, kind: kindFile, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: mode.Perm()})
					} else if !mode.IsDir() {
						entries = append(entries, entry{rel: path, kind: kindSpecial, mtime: info.ModTime().UnixNano(), mode: mode})
					}
					continue
				}
			}
			switch {
			case mode&fs.ModeSymlink != 0:
				add(RuleSymlink, path, "a symbolic link is not allowed")
				entries = append(entries, entry{rel: path, kind: kindLink, mtime: info.ModTime().UnixNano(), mode: mode})
			case mode.IsDir():
				if strings.HasPrefix(name, ".") {
					add(RuleHiddenFile, path, "the hidden directory %q is not allowed", name)
					continue
				}
				if err := visit(path); err != nil {
					return err
				}
			case mode.IsRegular():
				if strings.HasPrefix(name, ".") && name != KeepFileName {
					add(RuleHiddenFile, path, "the hidden file %q is not allowed; only .gitkeep is allowed", name)
				}
				if linkCount(info) > 1 {
					add(RuleHardlink, path, "a hard link is not allowed; the file has %d links", linkCount(info))
				}
				entries = append(entries, entry{rel: path, kind: kindFile, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: mode.Perm()})
			default:
				add(RuleSpecialFile, path, "only regular files are allowed")
				entries = append(entries, entry{rel: path, kind: kindSpecial, mtime: info.ModTime().UnixNano(), mode: mode})
			}
		}
		return nil
	}
	if err := visit(""); err != nil {
		return nil, nil, time.Time{}, err
	}
	if count > maxWalkEntries {
		add(RuleTooManyFiles, "", "the folder holds more than %d entries; the limit is %d files", maxWalkEntries, MaxFiles)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	return entries, issues, newest, nil
}

// checkPathSegment returns a message when the name of a file is not valid.
func checkPathSegment(name string) string {
	if !utf8.ValidString(name) {
		return "the file name is not valid UTF-8"
	}
	for _, r := range name {
		if r == '\\' || r == 0 || unicode.IsControl(r) {
			return fmt.Sprintf("the file name %q holds a control character or a backslash", name)
		}
	}
	return ""
}

// build reads the content of the files and checks the skill.
func build(dir, name string, entries []entry, folderIssues []Issue) (*Skill, error) {
	s := &Skill{Name: name, Dir: dir, text: map[string][]byte{}}
	s.Issues = append(s.Issues, folderIssues...)

	var total int64
	lines := make([]hashLine, 0, len(entries))
	for _, e := range entries {
		if e.kind != kindFile {
			lines = append(lines, hashLine{path: e.rel, sum: zeroSum})
			continue
		}
		total += e.size
		file := File{Path: e.rel, Size: e.size, Text: IsTextPath(e.rel)}
		full := filepath.Join(dir, filepath.FromSlash(e.rel))
		switch {
		case e.size > MaxFileBytes:
			s.Issues = append(s.Issues, Issue{Severity: SeverityError, Rule: RuleFileTooLarge, File: e.rel,
				Message: fmt.Sprintf("the file is %d bytes; the limit is %d bytes", e.size, MaxFileBytes)})
			sum := sha256.Sum256([]byte("oversize:" + strconv.FormatInt(e.size, 10)))
			file.SHA256 = hex.EncodeToString(sum[:])
			file.Text = false
		case file.Text:
			data, err := os.ReadFile(full) //nolint:gosec // The path comes from the walk of the skill folder.
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(data)
			file.SHA256 = hex.EncodeToString(sum[:])
			file.Size = int64(len(data))
			if !utf8.Valid(data) {
				s.Issues = append(s.Issues, Issue{Severity: SeverityError, Rule: RuleNotUTF8, File: e.rel,
					Message: "the file is not valid UTF-8 text; SKILL.md, references/ and scripts/ hold text only"})
				file.Text = false
			} else {
				s.text[e.rel] = data
			}
		default:
			sum, n, err := hashFile(full)
			if err != nil {
				return nil, err
			}
			file.SHA256 = sum
			file.Size = n
		}
		s.Files = append(s.Files, file)
		lines = append(lines, hashLine{path: e.rel, sum: file.SHA256})
		if file.Text && e.rel != FileName && e.size > MaxReadFileBytes {
			s.Issues = append(s.Issues, Issue{Severity: SeverityWarning, Rule: RuleReadTooLarge, File: e.rel,
				Message: fmt.Sprintf("the file is %d bytes; the tool read_skill_file returns a file of %d bytes or less", e.size, MaxReadFileBytes)})
		}
		if top, rest, ok := strings.Cut(e.rel, "/"); ok && top == DirReferences && strings.Contains(rest, "/") {
			s.Issues = append(s.Issues, Issue{Severity: SeverityWarning, Rule: RuleReferenceDepth, File: e.rel,
				Message: "the format recommends that references stay one level deep from references/"})
		}
	}
	s.Hash = hashLines(lines)

	if len(s.Files) > MaxFiles {
		s.Issues = append(s.Issues, Issue{Severity: SeverityError, Rule: RuleTooManyFiles,
			Message: fmt.Sprintf("the skill has %d files; the limit is %d", len(s.Files), MaxFiles)})
	}
	if total > MaxTotalBytes {
		s.Issues = append(s.Issues, Issue{Severity: SeverityError, Rule: RuleTotalTooLarge,
			Message: fmt.Sprintf("the files of the skill are %d bytes in total; the limit is %d bytes", total, MaxTotalBytes)})
	}

	raw, ok := s.text[FileName]
	if !ok {
		if _, listed := s.File(FileName); !listed {
			s.Issues = append(s.Issues, Issue{Severity: SeverityError, Rule: RuleSkillMissing, File: FileName,
				Message: "the folder has no file SKILL.md"})
		}
	} else {
		meta, body, issues := CheckSkillFile(name, raw)
		s.Meta, s.Body = meta, body
		s.Issues = append(s.Issues, issues...)
	}
	sort.SliceStable(s.Issues, func(i, j int) bool {
		a, b := s.Issues[i], s.Issues[j]
		if a.Severity != b.Severity {
			return a.Severity == SeverityError
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Rule < b.Rule
	})
	return s, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // The path comes from the walk of the skill folder.
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ReadVerified returns the content of one file of the skill, read from disk.
// It fails when the content no longer has the size and the SHA-256 that the
// read recorded: the skill has changed since.
func (s *Skill) ReadVerified(rel string) ([]byte, error) {
	file, ok := s.File(rel)
	if !ok {
		return nil, fmt.Errorf("skill %q has no file %q", s.Name, rel)
	}
	if data, ok := s.text[rel]; ok {
		return data, nil
	}
	if file.Size > MaxFileBytes {
		return nil, fmt.Errorf("skill %q: file %q is above the size limit", s.Name, rel)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(rel))) //nolint:gosec // rel is a path of the recorded list of files.
	if err != nil {
		return nil, fmt.Errorf("skill %q: %w", s.Name, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != file.SHA256 {
		return nil, fmt.Errorf("skill %q: file %q changed after Dagu read the skill (hash %s)", s.Name, rel, s.Hash)
	}
	return data, nil
}
