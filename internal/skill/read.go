// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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

// ErrFileTooLarge is returned when a file of the skill is above MaxFileBytes.
var ErrFileTooLarge = errors.New("the file is above the size limit")

// ErrFileChanged is returned when a file of the skill no longer has the content
// that the read of the skill recorded.
var ErrFileChanged = errors.New("the file changed after Dagu read the skill")

// errFolderChanged is returned when the skill folder is no longer the folder
// that the path named a moment before, for example because it became a link.
var errFolderChanged = errors.New("the skill folder changed while Dagu opened it")

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

// maxCacheEntries bounds the number of folders that the cache keeps.
const maxCacheEntries = 128

// maxCacheBytes bounds the memory that the cache keeps for all folders: the
// text of the files and the lists of files and issues.
const maxCacheBytes = 64 << 20

// cacheItemOverhead estimates the memory of one file or issue of a cached read
// beyond its strings.
const cacheItemOverhead = 64

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

// cache keeps the last result of Read for each folder, with the signature of
// the walk that gave it. A full cache drops entries in no particular order.
var cache = struct {
	mu      sync.Mutex
	entries map[string]cacheEntry
	bytes   int64
}{entries: map[string]cacheEntry{}}

type cacheEntry struct {
	signature string
	skill     *Skill
	bytes     int64
}

// Read reads the skill folder dir. The name of the skill is the name of the
// folder. It never fails on a violation of the format: the violations are
// issues of the result. It returns ErrNotFound when the folder does not exist.
//
// Read walks the folder on disk at each call. When the walk matches the walk of
// an earlier read, and no entry changed in the last 2 seconds, it returns the
// earlier result, which a bounded process cache keeps. Otherwise it reads the
// files. A folder that changes while Read reads it is read again. A folder that
// does not settle gives an error with the rule "changing".
//
// The caller MUST NOT change the result: the cache shares it.
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
		signature := signatureOf(before, folderIssues)
		if cached, ok := lookup(abs, signature, newest); ok {
			return cached, nil
		}
		s, err := readFiles(abs, name, before, folderIssues)
		if err != nil {
			unreadable = !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, errFolderChanged)
			continue
		}
		after, afterIssues, _, err := walk(abs)
		if err != nil || signatureOf(after, afterIssues) != signature {
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

// lookup returns the cached read of dir when its walk had the same signature.
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

// store keeps the read of dir, unless an entry of the folder is too recent.
func store(dir, signature string, newest time.Time, s *Skill) {
	if time.Since(newest) < cacheRacyWindow {
		return
	}
	size := cacheCost(signature, s)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if old, ok := cache.entries[dir]; ok {
		cache.bytes -= old.bytes
		delete(cache.entries, dir)
	}
	for k, e := range cache.entries {
		if len(cache.entries) < maxCacheEntries && cache.bytes+size <= maxCacheBytes {
			break
		}
		cache.bytes -= e.bytes
		delete(cache.entries, k)
	}
	cache.entries[dir] = cacheEntry{signature: signature, skill: s, bytes: size}
	cache.bytes += size
}

// cacheCost estimates the memory of a cached read. A folder of many files
// with long paths holds little text, so the text alone would not bound it.
func cacheCost(signature string, s *Skill) int64 {
	n := len(signature) + len(s.Body) + maxFrontMatterBytes
	for _, data := range s.text {
		n += len(data)
	}
	for _, f := range s.Files {
		n += len(f.Path) + len(f.SHA256) + cacheItemOverhead
	}
	for _, i := range s.Issues {
		n += len(i.Rule) + len(i.File) + len(i.Message) + cacheItemOverhead
	}
	return int64(n)
}

func emptyHash() string { return hashLines(nil) }

// signatureOf identifies one walk: the entries and the issues of the layout. A
// directory or a link count is not part of an entry, so its issue stands for
// it.
func signatureOf(entries []entry, issues []Issue) string {
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
	// The values are quoted, so the key is unambiguous.
	for _, i := range issues {
		b.WriteString(strconv.Quote(i.Rule))
		b.WriteString(strconv.Quote(i.File))
		b.WriteString(strconv.Quote(i.Message))
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
	checkLinks := func(path string, info fs.FileInfo) error {
		n, err := linkCount(filepath.Join(dir, filepath.FromSlash(path)), info)
		if err != nil {
			return err
		}
		if n > 1 {
			add(RuleHardlink, path, "a hard link is not allowed; the file has %d links", n)
		}
		return nil
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
					if err := checkLinks(path, info); err != nil {
						return err
					}
					entries = append(entries, entry{rel: path, kind: kindFile, size: info.Size(), mtime: info.ModTime().UnixNano(), mode: mode.Perm()})
					continue
				case (name == DirReferences || name == DirAssets || name == DirScripts) && mode.IsDir():
					if err := visit(path); err != nil {
						return err
					}
					continue
				case mode&fs.ModeSymlink != 0:
					// Whatever its name, a link gets the rule symlink in the switch below.
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
				if err := checkLinks(path, info); err != nil {
					return err
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
		add(RuleTooManyFiles, "", "the folder holds more than %d entries (files and directories); the limit is %d entries", maxWalkEntries, maxWalkEntries)
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

// readFiles is build. A test wraps it to change the folder between the two
// walks of Read.
var readFiles = build

// build reads the content of the files and checks the skill.
func build(dir, name string, entries []entry, folderIssues []Issue) (*Skill, error) {
	root, err := openFolder(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	s := &Skill{Name: name, Dir: dir, text: map[string][]byte{}}
	s.Issues = append(s.Issues, folderIssues...)

	var total, kept int64
	lines := make([]hashLine, 0, len(entries))
	for _, e := range entries {
		if e.kind != kindFile {
			lines = append(lines, hashLine{path: e.rel, sum: zeroSum})
			continue
		}
		total += e.size
		file := File{Path: e.rel, Size: e.size, Text: IsTextPath(e.rel)}
		rel := filepath.FromSlash(e.rel)
		switch {
		case e.size > MaxFileBytes:
			s.Issues = append(s.Issues, Issue{Severity: SeverityError, Rule: RuleFileTooLarge, File: e.rel,
				Message: fmt.Sprintf("the file is %d bytes; the limit is %d bytes", e.size, MaxFileBytes)})
			sum := sha256.Sum256([]byte("oversize:" + strconv.FormatInt(e.size, 10)))
			file.SHA256 = hex.EncodeToString(sum[:])
			file.Text = false
		case file.Text:
			data, err := readAtMost(root, rel, e.size)
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
			} else if kept+file.Size <= MaxTotalBytes {
				// A folder above the total limit leaves the rest of its text on disk,
				// so one read never holds more than the limit.
				s.text[e.rel] = data
				kept += file.Size
			}
		default:
			sum, n, err := hashFile(root, rel)
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

// openFolder opens the skill folder dir. The root keeps every read inside the
// folder, also when a file becomes a link after the walk.
func openFolder(dir string) (*os.Root, error) {
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	// The root opens the target of a link, which is not the entry that Lstat saw.
	if opened, err := root.Stat("."); err != nil || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, errFolderChanged
	}
	return root, nil
}

// readAtMost reads at most size+1 bytes of a file of the skill, so a file that
// grows after the walk cannot fill the memory. The caller sees the extra byte
// as a change of the file.
func readAtMost(root *os.Root, rel string, size int64) ([]byte, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, size+1))
}

func hashFile(root *os.Root, rel string) (string, int64, error) {
	f, err := root.Open(rel)
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

// Open returns the content of one file of the skill as the read recorded it.
// The reader gives the recorded size, and the caller closes it. Open fails
// with ErrFileTooLarge for a file above MaxFileBytes, and with ErrFileChanged
// when the file is no longer a regular file of the recorded size. A file whose
// content changes while the caller reads it ends the reader with
// ErrFileChanged in place of its last bytes, so a complete read always has
// the recorded SHA-256.
func (s *Skill) Open(rel string) (io.ReadCloser, error) {
	file, ok := s.File(rel)
	if !ok {
		return nil, fmt.Errorf("skill %q has no file %q: %w", s.Name, rel, fs.ErrNotExist)
	}
	if data, ok := s.text[rel]; ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	if file.Size > MaxFileBytes {
		return nil, fmt.Errorf("skill %q: file %q: %w", s.Name, rel, ErrFileTooLarge)
	}
	changed := fmt.Errorf("skill %q: file %q: %w", s.Name, rel, ErrFileChanged)
	root, err := openFolder(s.Dir)
	if err != nil {
		return nil, openFailure(changed, err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.FromSlash(rel)
	// A link or a special file is never opened.
	before, err := root.Lstat(name)
	if err != nil {
		return nil, openFailure(changed, err)
	}
	if !before.Mode().IsRegular() || before.Size() != file.Size {
		return nil, changed
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, openFailure(changed, err)
	}
	if opened, err := f.Stat(); err != nil || !os.SameFile(before, opened) {
		_ = f.Close()
		return nil, changed
	}
	return &checkedFile{f: f, sum: sha256.New(), left: file.Size, want: file.SHA256}, nil
}

// openFailure gives changed for a file or a folder that is gone or replaced,
// and the error itself otherwise.
func openFailure(changed, err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errFolderChanged) {
		return changed
	}
	return err
}

// checkedFile reads the recorded size of a file and checks its SHA-256 before
// it gives the last bytes.
type checkedFile struct {
	f    *os.File
	sum  hash.Hash
	left int64
	want string
	err  error
}

func (c *checkedFile) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.left == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.f.Read(p)
	c.sum.Write(p[:n])
	c.left -= int64(n)
	switch {
	case c.left == 0 && hex.EncodeToString(c.sum.Sum(nil)) != c.want:
		c.err = ErrFileChanged
		return 0, c.err
	case c.left == 0:
		return n, nil
	case errors.Is(err, io.EOF):
		// The file is shorter than the read recorded.
		c.err = ErrFileChanged
		return n, c.err
	}
	return n, err
}

func (c *checkedFile) Close() error { return c.f.Close() }
