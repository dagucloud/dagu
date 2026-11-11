// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
	// lockDirName holds the lock directories of the writers. It is hidden, so
	// that the listing of skills ignores it.
	lockDirName = ".locks"
	lockTimeout = 15 * time.Second
	// lockStaleThreshold is the age after which another writer takes a lock that
	// its holder no longer refreshes.
	lockStaleThreshold = 30 * time.Second
)

// ErrPreconditionRequired is returned for a write that has no precondition.
var ErrPreconditionRequired = errors.New("a write needs a precondition: the hash that the client read, or the rule that the skill must not exist")

// ConflictError is returned when the precondition of a write fails: the skill
// changed after the client read it, or it exists when it must not.
type ConflictError struct {
	// Current is the hash of the skill now. It is empty when the skill does not
	// exist.
	Current string
	// Message tells why the write is refused.
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// InvalidError is returned when the content of a write breaks a rule.
type InvalidError struct{ Issues []Issue }

func (e *InvalidError) Error() string {
	parts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		parts[i] = issue.String()
	}
	return strings.Join(parts, "; ")
}

// Precondition is the condition of a write. Exactly one field is set.
type Precondition struct {
	// IfMatch is the hash that the client read. The write applies only when the
	// skill still has this hash.
	IfMatch string
	// MustNotExist makes the write apply only when the skill folder does not
	// exist. It is the condition of the creation of a skill.
	MustNotExist bool
}

// Store reads and writes the skill folders of one skills directory. A Store
// holds no state of the skills: every call reads the disk. Several Stores,
// in several processes, can use the same directory.
type Store struct {
	root string
}

// NewStore returns the store of the skills directory root.
func NewStore(root string) *Store { return &Store{root: root} }

// Root returns the skills directory.
func (s *Store) Root() string { return s.root }

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

// CheckPath checks a path of a file of a skill for a write. It returns the
// clean path.
func CheckPath(rel string) (string, error) {
	if rel == "" || len(rel) > MaxPathLength || !utf8.ValidString(rel) {
		return "", &InvalidError{Issues: []Issue{pathIssue(rel, "the path must have 1 to %d bytes of UTF-8", MaxPathLength)}}
	}
	if !isCleanRelative(rel) {
		return "", &InvalidError{Issues: []Issue{pathIssue(rel, "the path must be relative, with / separators, and without . or .. parts")}}
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if msg := checkPathSegment(p); msg != "" {
			return "", &InvalidError{Issues: []Issue{pathIssue(rel, "%s", msg)}}
		}
	}
	// The reader skips such a file, so it would be outside the hash and the quota.
	if tempNamePattern.MatchString(parts[len(parts)-1]) {
		return "", &InvalidError{Issues: []Issue{pathIssue(rel, "the name has the form of a temporary file (.tmp.<number>)")}}
	}
	if rel == FileName {
		return rel, nil
	}
	if len(parts) < 2 || (parts[0] != DirReferences && parts[0] != DirAssets && parts[0] != DirScripts) {
		return "", &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RuleRootEntry, File: rel,
			Message: "a file must be SKILL.md or lie under references/, assets/ or scripts/"}}}
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, ".") && (p != KeepFileName || p != parts[len(parts)-1]) {
			return "", &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RuleHiddenFile, File: rel,
				Message: "a hidden file is not allowed; only .gitkeep is allowed"}}}
		}
	}
	return rel, nil
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
		if p == lockDirName {
			return "", &InvalidError{Issues: []Issue{pathIssue(rel, "reserved name")}}
		}
	}
	return rel, nil
}

func pathIssue(rel, format string, args ...any) Issue {
	return Issue{Severity: SeverityError, Rule: RulePath, File: rel, Message: fmt.Sprintf(format, args...)}
}

// WriteFile writes one file of a skill. The write is atomic for the file. The
// precondition decides whether the write applies. The creation of a skill is a
// write of SKILL.md with MustNotExist. WriteFile returns the skill as it is
// after the write.
//
// To save several files, write the files first and SKILL.md last: a reader
// that sees a skill with a new file and the old SKILL.md reads a complete
// skill, and the hash tells the version.
func (s *Store) WriteFile(ctx context.Context, name, rel string, data []byte, pre Precondition) (*Skill, error) {
	rel, err := CheckPath(rel)
	if err != nil {
		return nil, err
	}
	if !IsSafeFolderName(name) {
		return nil, ErrNotFound
	}
	if len(data) > MaxFileBytes {
		return nil, &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RuleFileTooLarge, File: rel,
			Message: fmt.Sprintf("the file is %d bytes; the limit is %d bytes", len(data), MaxFileBytes)}}}
	}
	if IsTextPath(rel) && !utf8.Valid(data) {
		return nil, &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RuleNotUTF8, File: rel,
			Message: "the file is not valid UTF-8 text; SKILL.md, references/ and scripts/ hold text only"}}}
	}
	if rel == FileName {
		_, _, issues := CheckSkillFile(name, data)
		if errs := onlyErrors(issues); len(errs) > 0 {
			return nil, &InvalidError{Issues: errs}
		}
	}

	var result *Skill
	err = s.withLock(ctx, name, pre, func() error {
		current, err := s.checkPrecondition(name, pre)
		if err != nil {
			return err
		}
		if current == nil && rel != FileName {
			return fmt.Errorf("%w: create the skill with SKILL.md first", ErrNotFound)
		}
		if err := checkQuota(current, rel, int64(len(data))); err != nil {
			return err
		}
		if current == nil {
			if err := os.MkdirAll(s.Dir(name), dirPerm); err != nil {
				return fmt.Errorf("create the skill folder: %w", err)
			}
		}
		root, err := s.openSkillRoot(name)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		if err := writeInRoot(root, rel, data); err != nil {
			return err
		}
		result, err = Read(s.Dir(name))
		return err
	})
	return result, err
}

// linkRefusal is the error of a write or a delete that would go through a symbolic
// link. The rule is the rule that Read reports for the same entry.
func linkRefusal(rule, rel, format string, args ...any) error {
	return &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: rule, File: rel, Message: fmt.Sprintf(format, args...)}}}
}

// openSkillRoot opens the folder of a skill for a write or a delete. Every later
// operation stays inside this folder: the Go root refuses a path that leaves it, also
// when another process swaps a folder for a link while the operation runs. It refuses
// a skill folder that is a symbolic link.
func (s *Store) openSkillRoot(name string) (*os.Root, error) {
	dir := s.Dir(name)
	before, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if before.Mode()&fs.ModeSymlink != 0 {
		return nil, linkRefusal(RuleFolderLink, "", "the skill folder is a symbolic link; Dagu never writes through a symbolic link")
	}
	if !before.IsDir() {
		return nil, ErrNotFound
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	// The folder can become a link between the two calls. The root then opens the
	// target of the link, which is not the folder that Lstat saw.
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, linkRefusal(RuleFolderLink, "", "the skill folder changed while Dagu opened it")
	}
	return root, nil
}

// checkNoLink returns an error when a component of rel, from the top folder to the
// entry, is a symbolic link. The entry can be a link when leafMayBeLink is true. A
// component that does not exist ends the check: nothing below it can be a link.
func checkNoLink(root *os.Root, rel string, leafMayBeLink bool) error {
	parts := strings.Split(rel, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(sub)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 && (!leafMayBeLink || i != len(parts)-1) {
			return linkRefusal(RuleSymlink, rel, "the path goes through the symbolic link %q; Dagu never writes through a symbolic link", sub)
		}
	}
	return nil
}

// writeInRoot durably replaces the file rel of the folder of root. It writes a
// temporary file next to the target and renames it, as WriteFileAtomic does. It
// checks the path for links before the write and again right before the rename.
func writeInRoot(root *os.Root, rel string, data []byte) error {
	if err := checkNoLink(root, rel, false); err != nil {
		return err
	}
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if err := root.Mkdir(strings.Join(parts[:i], "/"), dirPerm); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create the folder of %s: %w", rel, err)
		}
	}
	// The name matches tempNamePattern, so a reader ignores the file.
	temp := fmt.Sprintf("%s.tmp.%d", rel, rand.Uint64()) //nolint:gosec // the name of a temporary file, not a secret
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	fail := func(err error) error {
		_ = file.Close()
		_ = root.Remove(temp)
		return fmt.Errorf("write %s: %w", rel, err)
	}
	if _, err := file.Write(data); err != nil {
		return fail(err)
	}
	if err := file.Chmod(filePerm); err != nil {
		return fail(err)
	}
	if err := file.Sync(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		_ = root.Remove(temp)
		return fmt.Errorf("write %s: %w", rel, err)
	}
	if err := checkNoLink(root, rel, false); err != nil {
		_ = root.Remove(temp)
		return err
	}
	if err := renameInRoot(root, temp, rel); err != nil {
		_ = root.Remove(temp)
		return fmt.Errorf("write %s: %w", rel, err)
	}
	if err := fileutil.SyncDir(filepath.Join(root.Name(), filepath.FromSlash(path.Dir(rel)))); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// renameInRoot renames a file inside the root. It retries a transient error of
// Windows, as fileutil.ReplaceFile does.
func renameInRoot(root *os.Root, from, to string) error {
	err := root.Rename(from, to)
	for attempt := 0; attempt < 5 && err != nil && fileutil.IsTransientFileError(err); attempt++ {
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
		err = root.Rename(from, to)
	}
	return err
}

// checkQuota checks the number and the size of the files after a write.
func checkQuota(current *Skill, rel string, size int64) error {
	if current == nil {
		return nil
	}
	count, total := 1, size
	for _, f := range current.Files {
		if f.Path == rel {
			continue
		}
		count++
		total += f.Size
	}
	if count > MaxFiles {
		return &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RuleTooManyFiles, File: rel,
			Message: fmt.Sprintf("the write gives %d files; the limit is %d", count, MaxFiles)}}}
	}
	if total > MaxTotalBytes {
		return &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RuleTotalTooLarge, File: rel,
			Message: fmt.Sprintf("the write gives %d bytes in total; the limit is %d bytes", total, MaxTotalBytes)}}}
	}
	return nil
}

// DeleteFile deletes one entry of a skill folder, then the folders that become
// empty inside references/, assets/ and scripts/. The entry can be a file that
// breaks the layout: the owner removes it to repair the skill. It returns the
// skill after the delete. SKILL.md cannot be deleted alone: delete the skill.
func (s *Store) DeleteFile(ctx context.Context, name, rel string, pre Precondition) (*Skill, error) {
	rel, err := CheckExistingPath(rel)
	if err != nil {
		return nil, err
	}
	if rel == FileName {
		return nil, &InvalidError{Issues: []Issue{{Severity: SeverityError, Rule: RulePath, File: rel,
			Message: "SKILL.md cannot be deleted alone; delete the skill"}}}
	}
	if !IsSafeFolderName(name) {
		return nil, ErrNotFound
	}
	var result *Skill
	err = s.withLock(ctx, name, pre, func() error {
		current, err := s.checkPrecondition(name, pre)
		if err != nil {
			return err
		}
		if current == nil {
			return ErrNotFound
		}
		root, err := s.openSkillRoot(name)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		// A link at the end of the path is the entry that the owner removes to repair
		// the skill. The delete removes the link and never its target.
		if err := checkNoLink(root, rel, true); err != nil {
			return err
		}
		info, err := root.Lstat(rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: no file %s", ErrNotFound, rel)
			}
			return err
		}
		if info.IsDir() {
			// A directory that the layout does not allow, or a whole folder.
			err = root.RemoveAll(rel)
		} else {
			err = root.Remove(rel)
		}
		if err != nil {
			return fmt.Errorf("delete %s: %w", rel, err)
		}
		// Remove the parents that are empty, up to the top directory. Then sync the
		// nearest parent that still exists.
		top, _, _ := strings.Cut(rel, "/")
		dir := path.Dir(rel)
		for dir != top && dir != "." {
			if root.Remove(dir) != nil {
				break
			}
			dir = path.Dir(dir)
		}
		if err := fileutil.SyncDir(filepath.Join(root.Name(), filepath.FromSlash(dir))); err != nil {
			return fmt.Errorf("delete %s: %w", rel, err)
		}
		result, err = Read(s.Dir(name))
		return err
	})
	return result, err
}

// DeleteSkill deletes the whole folder of a skill, with every file.
func (s *Store) DeleteSkill(ctx context.Context, name string, pre Precondition) error {
	if !IsSafeFolderName(name) {
		return ErrNotFound
	}
	if pre.MustNotExist {
		return ErrPreconditionRequired
	}
	return s.withLock(ctx, name, pre, func() error {
		current, err := s.checkPrecondition(name, pre)
		if err != nil {
			return err
		}
		if current == nil {
			return ErrNotFound
		}
		if err := os.RemoveAll(s.Dir(name)); err != nil {
			return fmt.Errorf("delete the skill folder: %w", err)
		}
		return fileutil.SyncDir(s.root)
	})
}

// checkPrecondition reads the skill and compares it with the precondition. It
// returns the current skill, or nil when the folder does not exist.
func (s *Store) checkPrecondition(name string, pre Precondition) (*Skill, error) {
	if !pre.MustNotExist && pre.IfMatch == "" {
		return nil, ErrPreconditionRequired
	}
	current, err := Read(s.Dir(name))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrNotFound) {
		current = nil
	}
	if pre.MustNotExist {
		if current != nil {
			return nil, &ConflictError{Current: current.Hash, Message: fmt.Sprintf("the skill %q already exists", name)}
		}
		return nil, nil
	}
	if current == nil {
		return nil, ErrNotFound
	}
	if current.Hash != pre.IfMatch {
		return nil, &ConflictError{Current: current.Hash,
			Message: fmt.Sprintf("the skill %q changed after the client read it (current hash %s)", name, current.Hash)}
	}
	return current, nil
}

// withLock runs fn while it holds the lock of the skill. The lock is a
// directory lock in the skills directory, so it works between processes and
// between instances that share the directory. A crashed holder is detected by
// the stale threshold of the lock.
func (s *Store) withLock(ctx context.Context, name string, pre Precondition, fn func() error) error {
	// A change of a skill that does not exist ends before the lock, so a request
	// for any name makes no lock folder.
	if !pre.MustNotExist {
		if pre.IfMatch == "" {
			return ErrPreconditionRequired
		}
		if _, err := os.Lstat(s.Dir(name)); errors.Is(err, fs.ErrNotExist) {
			return ErrNotFound
		}
	}
	lockDir := filepath.Join(s.root, lockDirName, name)
	if err := os.MkdirAll(lockDir, dirPerm); err != nil {
		return fmt.Errorf("create the lock folder: %w", err)
	}
	lock := dirlock.New(lockDir, &dirlock.LockOptions{StaleThreshold: lockStaleThreshold})
	lctx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	if err := lock.Lock(lctx); err != nil {
		return &ConflictError{Message: fmt.Sprintf("another writer holds the skill %q", name)}
	}
	defer func() { _ = lock.Unlock() }()
	// A slow write on shared storage can last longer than the stale threshold. The
	// heartbeat keeps the lock fresh, so another instance does not take it.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(lockStaleThreshold / 3)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_ = lock.Heartbeat(ctx)
			}
		}
	}()
	return fn()
}

func onlyErrors(issues []Issue) []Issue {
	var out []Issue
	for _, i := range issues {
		if i.Severity == SeverityError {
			out = append(out, i)
		}
	}
	return out
}
