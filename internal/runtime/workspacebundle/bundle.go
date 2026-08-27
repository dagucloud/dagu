// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspacebundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	DefaultMaxCompressedSize   int64 = 64 << 20
	DefaultMaxUncompressedSize int64 = 256 << 20
	DefaultMaxFiles                  = 8192

	archiveExt                 = ".tar.gz"
	storeLockHeartbeatInterval = 10 * time.Second
)

var (
	errPathEscapesBundle = errors.New("path escapes workspace bundle")
	errInvalidBundle     = errors.New("invalid workspace bundle")
	zeroTime             = time.Unix(0, 0).UTC()
)

type Descriptor struct {
	Digest      string
	Size        int64
	DAGPath     string
	OriginalRef string
	ResolvedRef string
}

type Limits struct {
	MaxCompressedSize   int64
	MaxUncompressedSize int64
	MaxFiles            int
}

type PackOptions struct {
	DAGPath     string
	DAGData     []byte
	Includes    []string
	OriginalRef string
	ResolvedRef string
	Limits      Limits
}

func DefaultLimits() Limits {
	return Limits{
		MaxCompressedSize:   DefaultMaxCompressedSize,
		MaxUncompressedSize: DefaultMaxUncompressedSize,
		MaxFiles:            DefaultMaxFiles,
	}
}

func normalizeLimits(l Limits) Limits {
	defaults := DefaultLimits()
	if l.MaxCompressedSize <= 0 {
		l.MaxCompressedSize = defaults.MaxCompressedSize
	}
	if l.MaxUncompressedSize <= 0 {
		l.MaxUncompressedSize = defaults.MaxUncompressedSize
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = defaults.MaxFiles
	}
	return l
}

func PackDirectory(root string, opts PackOptions) (*Descriptor, []byte, error) {
	var buf bytes.Buffer
	desc, err := packDirectory(root, opts, &buf)
	if err != nil {
		return nil, nil, err
	}
	return desc, buf.Bytes(), nil
}

// PackDirectoryToFile creates a workspace archive in stagingDir.
func PackDirectoryToFile(root, stagingDir string, opts PackOptions) (*Descriptor, string, error) {
	stagingDir = filepath.Clean(strings.TrimSpace(stagingDir))
	if stagingDir == "." || stagingDir == "" {
		return nil, "", fmt.Errorf("workspace bundle staging directory is required")
	}
	if err := os.MkdirAll(stagingDir, 0o750); err != nil {
		return nil, "", fmt.Errorf("create workspace bundle staging directory: %w", err)
	}
	file, err := os.CreateTemp(stagingDir, ".workspace-*.tar.gz")
	if err != nil {
		return nil, "", fmt.Errorf("create workspace bundle: %w", err)
	}
	archivePath := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = fileutil.Remove(archivePath)
		}
	}()
	desc, err := packDirectory(root, opts, file)
	if err != nil {
		return nil, "", err
	}
	if err := file.Close(); err != nil {
		return nil, "", fmt.Errorf("close workspace bundle: %w", err)
	}
	cleanup = false
	return desc, archivePath, nil
}

func packDirectory(root string, opts PackOptions, dest io.Writer) (*Descriptor, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root must be a directory")
	}

	dagPath, err := NormalizeRelativePath(opts.DAGPath)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace DAG path: %w", err)
	}

	limits := normalizeLimits(opts.Limits)
	var files []string
	if len(opts.Includes) > 0 {
		files, err = collectSelectedFiles(root, opts.Includes, limits)
	} else {
		files, err = collectFiles(root, limits)
	}
	if err != nil {
		return nil, err
	}
	files = appendUniquePath(files, dagPath)
	if len(files) > limits.MaxFiles {
		return nil, fmt.Errorf("workspace bundle exceeds file count limit %d", limits.MaxFiles)
	}

	hasher := sha256.New()
	written := &limitedWriter{w: io.MultiWriter(dest, hasher), max: limits.MaxCompressedSize}
	gz := gzip.NewWriter(written)
	gz.Name = ""
	gz.Comment = ""
	gz.ModTime = zeroTime
	tw := tar.NewWriter(gz)

	var unpacked int64
	for _, rel := range files {
		if rel == dagPath && opts.DAGData != nil {
			unpacked += int64(len(opts.DAGData))
			if unpacked > limits.MaxUncompressedSize {
				_ = tw.Close()
				_ = gz.Close()
				return nil, fmt.Errorf("workspace bundle exceeds uncompressed size limit %d", limits.MaxUncompressedSize)
			}
			header := &tar.Header{
				Name:       rel,
				Mode:       0o644,
				Size:       int64(len(opts.DAGData)),
				Typeflag:   tar.TypeReg,
				ModTime:    zeroTime,
				AccessTime: zeroTime,
				ChangeTime: zeroTime,
				Format:     tar.FormatPAX,
			}
			if err := tw.WriteHeader(header); err != nil {
				_ = tw.Close()
				_ = gz.Close()
				return nil, fmt.Errorf("write tar header for %q: %w", rel, err)
			}
			if _, err := tw.Write(opts.DAGData); err != nil {
				_ = tw.Close()
				_ = gz.Close()
				return nil, fmt.Errorf("write bundle file %q: %w", rel, err)
			}
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("stat bundle file %q: %w", rel, err)
		}
		if err := validatePackFile(rel, info); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, err
		}
		unpacked += info.Size()
		if unpacked > limits.MaxUncompressedSize {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("workspace bundle exceeds uncompressed size limit %d", limits.MaxUncompressedSize)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("create tar header for %q: %w", rel, err)
		}
		header.Name = rel
		header.ModTime = zeroTime
		header.AccessTime = zeroTime
		header.ChangeTime = zeroTime
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		header.Format = tar.FormatPAX
		if err := tw.WriteHeader(header); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("write tar header for %q: %w", rel, err)
		}
		if info.IsDir() {
			continue
		}
		file, err := os.Open(abs) //nolint:gosec
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("open bundle file %q: %w", rel, err)
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("write bundle file %q: %w", rel, copyErr)
		}
		if closeErr != nil {
			_ = tw.Close()
			_ = gz.Close()
			return nil, fmt.Errorf("close bundle file %q: %w", rel, closeErr)
		}
	}

	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return nil, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}

	return &Descriptor{
		Digest:      hex.EncodeToString(hasher.Sum(nil)),
		Size:        written.written,
		DAGPath:     dagPath,
		OriginalRef: strings.TrimSpace(opts.OriginalRef),
		ResolvedRef: strings.TrimSpace(opts.ResolvedRef),
	}, nil
}

func Extract(data []byte, dest string, desc Descriptor, limits Limits) error {
	if err := Verify(data, desc.Digest); err != nil {
		return err
	}
	limits = normalizeLimits(limits)
	if int64(len(data)) > limits.MaxCompressedSize {
		return fmt.Errorf("workspace bundle exceeds compressed size limit %d", limits.MaxCompressedSize)
	}
	dest = filepath.Clean(strings.TrimSpace(dest))
	if dest == "" {
		return fmt.Errorf("workspace destination is required")
	}

	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create workspace parent: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".workspace-*")
	if err != nil {
		return fmt.Errorf("create temporary workspace: %w", err)
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = fileutil.RemoveAll(tmp)
		}
	}()

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open workspace bundle: %w", err)
	}
	gzClosed := false
	defer func() {
		if !gzClosed {
			_ = gz.Close()
		}
	}()

	tr := tar.NewReader(gz)
	var files int
	var unpacked int64
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read workspace bundle: %w", err)
		}
		rel, err := NormalizeRelativePath(header.Name)
		if err != nil {
			return fmt.Errorf("invalid workspace bundle path %q: %w", header.Name, err)
		}
		files++
		if files > limits.MaxFiles {
			return fmt.Errorf("workspace bundle exceeds file count limit %d", limits.MaxFiles)
		}
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if !IsPathWithin(tmp, target) {
			return errPathEscapesBundle
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, modePerm(header.FileInfo().Mode(), 0o755)); err != nil {
				return fmt.Errorf("create workspace directory %q: %w", rel, err)
			}
		case tar.TypeReg:
			unpacked += header.Size
			if unpacked > limits.MaxUncompressedSize {
				return fmt.Errorf("workspace bundle exceeds uncompressed size limit %d", limits.MaxUncompressedSize)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return fmt.Errorf("create workspace file parent %q: %w", rel, err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, modePerm(header.FileInfo().Mode(), 0o644)) //nolint:gosec // target is normalized and verified within tmp before opening.
			if err != nil {
				return fmt.Errorf("create workspace file %q: %w", rel, err)
			}
			_, copyErr := io.CopyN(file, tr, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract workspace file %q: %w", rel, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close workspace file %q: %w", rel, closeErr)
			}
		default:
			return fmt.Errorf("workspace bundle contains unsupported entry %q", rel)
		}
	}

	if err := gz.Close(); err != nil {
		return fmt.Errorf("close workspace bundle: %w", err)
	}
	gzClosed = true

	if _, err := os.Stat(filepath.Join(tmp, filepath.FromSlash(desc.DAGPath))); err != nil {
		return fmt.Errorf("workspace bundle DAG %q is missing: %w", desc.DAGPath, err)
	}
	if err := fileutil.RemoveAll(dest); err != nil {
		return fmt.Errorf("remove existing workspace: %w", err)
	}
	if err := fileutil.Rename(tmp, dest); err != nil {
		return fmt.Errorf("install workspace: %w", err)
	}
	cleanupTmp = false
	return nil
}

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func Verify(data []byte, digest string) error {
	if !ValidDigest(digest) {
		return fmt.Errorf("invalid workspace bundle digest %q", digest)
	}
	actual := Digest(data)
	if actual != digest {
		return fmt.Errorf("workspace bundle digest mismatch: got %s, want %s", actual, digest)
	}
	return nil
}

func ValidDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func NormalizeRelativePath(relPath string) (string, error) {
	relPath = strings.TrimSpace(relPath)
	if relPath == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(relPath) || path.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative")
	}
	slashPath := path.Clean(strings.ReplaceAll(relPath, `\`, "/"))
	if slashPath == "." || slashPath == ".." || strings.HasPrefix(slashPath, "../") {
		return "", errPathEscapesBundle
	}
	return slashPath, nil
}

func IsPathWithin(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func collectFiles(root string, limits Limits) ([]string, error) {
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if shouldSkip(slashRel, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := validatePackFile(slashRel, info); err != nil {
			return err
		}
		files = append(files, slashRel)
		if len(files) > limits.MaxFiles {
			return fmt.Errorf("workspace bundle exceeds file count limit %d", limits.MaxFiles)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect workspace files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func collectSelectedFiles(root string, includes []string, limits Limits) ([]string, error) {
	selected := make(map[string]struct{})
	rootFS := os.DirFS(root)
	for _, include := range includes {
		pattern, err := NormalizeRelativePath(include)
		if err != nil {
			return nil, fmt.Errorf("invalid workspace include %q: %w", include, err)
		}
		if !doublestar.ValidatePattern(pattern) {
			return nil, fmt.Errorf("invalid workspace include pattern %q", include)
		}
		matches, err := doublestar.Glob(rootFS, pattern, doublestar.WithFailOnIOErrors())
		if err != nil {
			return nil, fmt.Errorf("match workspace include %q: %w", include, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("workspace include %q matched no files", include)
		}
		for _, match := range matches {
			if err := collectSelectedPath(root, match, selected, limits.MaxFiles); err != nil {
				return nil, fmt.Errorf("collect workspace include %q: %w", include, err)
			}
		}
	}

	files := make([]string, 0, len(selected))
	for rel := range selected {
		files = append(files, rel)
	}
	sort.Strings(files)
	if len(files) > limits.MaxFiles {
		return nil, fmt.Errorf("workspace bundle exceeds file count limit %d", limits.MaxFiles)
	}
	return files, nil
}

func collectSelectedPath(root, rel string, selected map[string]struct{}, maxFiles int) error {
	rel, err := NormalizeRelativePath(rel)
	if err != nil {
		return err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if shouldSkip(rel, fs.FileInfoToDirEntry(info)) {
		return fmt.Errorf("workspace bundle does not support .git path %q", rel)
	}
	if err := validatePackFile(rel, info); err != nil {
		return err
	}
	if err := addSelectedPath(selected, rel, maxFiles); err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(abs, func(pathname string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		child, err := filepath.Rel(root, pathname)
		if err != nil {
			return err
		}
		child = filepath.ToSlash(child)
		if shouldSkip(child, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		childInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if err := validatePackFile(child, childInfo); err != nil {
			return err
		}
		return addSelectedPath(selected, child, maxFiles)
	})
}

func addSelectedPath(selected map[string]struct{}, rel string, maxFiles int) error {
	selected[rel] = struct{}{}
	if len(selected) > maxFiles {
		return fmt.Errorf("workspace bundle exceeds file count limit %d", maxFiles)
	}
	return nil
}

func appendUniquePath(paths []string, rel string) []string {
	if slices.Contains(paths, rel) {
		return paths
	}
	paths = append(paths, rel)
	sort.Strings(paths)
	return paths
}

func validatePackFile(rel string, info fs.FileInfo) error {
	if _, err := NormalizeRelativePath(rel); err != nil {
		return fmt.Errorf("invalid workspace path %q: %w", rel, err)
	}
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace bundle does not support symlink %q", rel)
	}
	if !mode.IsRegular() && !mode.IsDir() {
		return fmt.Errorf("workspace bundle does not support special file %q", rel)
	}
	return nil
}

func shouldSkip(rel string, entry fs.DirEntry) bool {
	name := entry.Name()
	if name == ".git" {
		return true
	}
	return strings.HasPrefix(rel, ".git/")
}

func modePerm(mode fs.FileMode, fallback fs.FileMode) fs.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return fallback
	}
	return perm
}

type limitedWriter struct {
	w       io.Writer
	max     int64
	written int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.max-w.written {
		return 0, fmt.Errorf("workspace bundle exceeds compressed size limit %d", w.max)
	}
	n, err := w.w.Write(p)
	w.written += int64(n)
	return n, err
}

func StoreDir(dataDir string) string {
	return filepath.Join(dataDir, "workspace-bundles")
}

type Store struct {
	dir                   string
	limits                Limits
	lock                  dirlock.DirLock
	lockHeartbeatInterval time.Duration
	gate                  chan struct{}
}

type Client interface {
	PutWorkspaceBundle(ctx context.Context, desc Descriptor, data []byte) error
	GetWorkspaceBundle(ctx context.Context, digest string) ([]byte, error)
}

func NewStore(dir string, limits Limits) *Store {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &Store{
		dir:                   dir,
		limits:                normalizeLimits(limits),
		lock:                  dirlock.New(dir, nil),
		lockHeartbeatInterval: storeLockHeartbeatInterval,
		gate:                  gate,
	}
}

func (s *Store) Put(ctx context.Context, desc Descriptor, data []byte) error {
	if strings.TrimSpace(s.dir) == "" {
		return fmt.Errorf("workspace bundle store is not configured")
	}
	return s.PutReader(ctx, desc, bytes.NewReader(data))
}

// PutReader stores a workspace bundle read from reader.
func (s *Store) PutReader(ctx context.Context, desc Descriptor, reader io.Reader) error {
	if strings.TrimSpace(s.dir) == "" {
		return fmt.Errorf("workspace bundle store is not configured")
	}
	path, err := s.path(desc.Digest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create workspace bundle store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bundle-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary workspace bundle: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = fileutil.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(reader, s.limits.MaxCompressedSize+1))
	if err != nil {
		return fmt.Errorf("write workspace bundle: %w", err)
	}
	if written > s.limits.MaxCompressedSize {
		return fmt.Errorf("workspace bundle exceeds compressed size limit %d", s.limits.MaxCompressedSize)
	}
	if desc.Size != 0 && desc.Size != written {
		return fmt.Errorf("workspace bundle size mismatch: got %d, want %d", written, desc.Size)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != desc.Digest {
		return fmt.Errorf("workspace bundle digest mismatch: got %s, want %s", actual, desc.Digest)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close workspace bundle: %w", err)
	}
	return s.withLock(ctx, func(lockCtx context.Context) error {
		if _, err := os.Stat(path); err == nil {
			if fileMatchesDigest(lockCtx, path, desc.Digest, s.limits.MaxCompressedSize) {
				if err := s.ensureLock(lockCtx); err != nil {
					return err
				}
				if err := touch(path); err != nil {
					return fmt.Errorf("refresh workspace bundle: %w", err)
				}
				return nil
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat workspace bundle: %w", err)
		}
		if err := s.ensureLock(lockCtx); err != nil {
			return err
		}
		if err := fileutil.ReplaceFile(tmpName, path); err != nil {
			return fmt.Errorf("commit workspace bundle: %w", err)
		}
		cleanup = false
		return nil
	})
}

func (s *Store) Get(ctx context.Context, digest string) ([]byte, error) {
	file, _, err := s.Open(ctx, digest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read workspace bundle: %w", err)
	}
	return data, nil
}

// Open opens a verified workspace bundle for reading.
func (s *Store) Open(ctx context.Context, digest string) (*os.File, int64, error) {
	if strings.TrimSpace(s.dir) == "" {
		return nil, 0, fmt.Errorf("workspace bundle store is not configured")
	}
	path, err := s.path(digest)
	if err != nil {
		return nil, 0, err
	}
	var file *os.File
	var size int64
	err = s.withLock(ctx, func(lockCtx context.Context) error {
		file, size, err = openVerified(lockCtx, path, digest, s.limits.MaxCompressedSize)
		if err != nil {
			return err
		}
		if err := s.ensureLock(lockCtx); err != nil {
			return err
		}
		if err := touch(path); err != nil {
			return fmt.Errorf("refresh workspace bundle: %w", err)
		}
		return nil
	})
	if err != nil {
		if file != nil {
			_ = file.Close()
		}
		return nil, 0, err
	}
	return file, size, nil
}

func (s *Store) Has(digest string) bool {
	path, err := s.path(digest)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func openVerified(ctx context.Context, path, digest string, maxSize int64) (*os.File, int64, error) {
	file, err := os.Open(path) //nolint:gosec // path is derived from a validated digest and configured store directory.
	if err != nil {
		return nil, 0, fmt.Errorf("open workspace bundle: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()

	hasher := sha256.New()
	size, err := io.Copy(hasher, io.LimitReader(&contextReader{ctx: ctx, reader: file}, maxSize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read workspace bundle: %w", err)
	}
	if size > maxSize {
		return nil, 0, fmt.Errorf("workspace bundle exceeds compressed size limit %d: %w", maxSize, errInvalidBundle)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != digest {
		return nil, 0, fmt.Errorf("workspace bundle digest mismatch: got %s, want %s: %w", actual, digest, errInvalidBundle)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("rewind workspace bundle: %w", err)
	}
	closeOnError = false
	return file, size, nil
}

func fileMatchesDigest(ctx context.Context, path, digest string, maxSize int64) bool {
	file, _, err := openVerified(ctx, path, digest, maxSize)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

// Touch reports whether a bundle exists and refreshes its expiration time.
func (s *Store) Touch(ctx context.Context, digest string) (bool, error) {
	if strings.TrimSpace(s.dir) == "" {
		return false, fmt.Errorf("workspace bundle store is not configured")
	}
	path, err := s.path(digest)
	if err != nil {
		return false, err
	}
	var exists bool
	err = s.withLock(ctx, func(lockCtx context.Context) error {
		file, _, err := openVerified(lockCtx, path, digest, s.limits.MaxCompressedSize)
		if err != nil {
			if cause := context.Cause(lockCtx); cause != nil {
				return cause
			}
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errInvalidBundle) {
				return nil
			}
			return err
		}
		defer func() { _ = file.Close() }()
		if err := s.ensureLock(lockCtx); err != nil {
			return err
		}
		if err := touch(path); err != nil {
			return fmt.Errorf("refresh workspace bundle: %w", err)
		}
		exists = true
		return nil
	})
	return exists, err
}

// Cleanup removes unprotected canonical bundles unused since before.
func (s *Store) Cleanup(ctx context.Context, before time.Time, protected map[string]struct{}) (int, error) {
	return s.CleanupReferenced(ctx, before, func(context.Context) (map[string]struct{}, error) {
		return protected, nil
	})
}

// CleanupReferenced removes expired bundles while preventing reference
// transitions. references must return every currently protected digest.
func (s *Store) CleanupReferenced(
	ctx context.Context,
	before time.Time,
	references func(context.Context) (map[string]struct{}, error),
) (int, error) {
	if strings.TrimSpace(s.dir) == "" {
		return 0, fmt.Errorf("workspace bundle store is not configured")
	}
	var removed int
	err := s.withLock(ctx, func(lockCtx context.Context) error {
		protected, err := references(lockCtx)
		if err != nil {
			return fmt.Errorf("list protected workspace bundles: %w", err)
		}

		var cleanupErrs []error
		walkErr := filepath.WalkDir(s.dir, func(bundlePath string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				if bundlePath == s.dir {
					return err
				}
				cleanupErrs = append(cleanupErrs, fmt.Errorf("inspect workspace bundle entry %q: %w", bundlePath, err))
				return nil
			}
			if cause := context.Cause(lockCtx); cause != nil {
				return cause
			}
			if entry.IsDir() {
				if bundlePath != s.dir && (entry.Name() == "staging" || dirlock.IsLockDirectoryName(entry.Name())) {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), archiveExt) {
				return nil
			}

			digest := strings.TrimSuffix(entry.Name(), archiveExt)
			canonicalPath, pathErr := s.path(digest)
			if pathErr != nil || filepath.Clean(bundlePath) != canonicalPath {
				return nil
			}
			if _, ok := protected[digest]; ok {
				return nil
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("inspect workspace bundle entry %q: %w", bundlePath, err))
				return nil
			}
			if !info.Mode().IsRegular() || !info.ModTime().Before(before) {
				return nil
			}
			if err := s.ensureLock(lockCtx); err != nil {
				return err
			}
			if err := fileutil.Remove(bundlePath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove expired workspace bundle: %w", err)
			}
			removed++
			return nil
		})
		if walkErr != nil {
			cleanupErrs = append(cleanupErrs, walkErr)
		}
		return errors.Join(cleanupErrs...)
	})
	if err != nil {
		return removed, fmt.Errorf("clean workspace bundles: %w", err)
	}
	return removed, nil
}

// WithLock runs fn while holding the shared bundle-store lock.
func (s *Store) WithLock(ctx context.Context, fn func(context.Context) error) error {
	return s.withLock(ctx, fn)
}

func (s *Store) withLock(ctx context.Context, fn func(context.Context) error) (retErr error) {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.gate:
	}
	defer func() { s.gate <- struct{}{} }()

	if err := s.lock.Lock(ctx); err != nil {
		return fmt.Errorf("lock workspace bundle store: %w", err)
	}
	if err := ctx.Err(); err != nil {
		if unlockErr := s.lock.Unlock(); unlockErr != nil {
			return errors.Join(err, fmt.Errorf("unlock workspace bundle store: %w", unlockErr))
		}
		return err
	}

	lockCtx, cancel := context.WithCancelCause(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	interval := s.lockHeartbeatInterval
	if interval <= 0 {
		interval = storeLockHeartbeatInterval
	}
	// Keep the shared lease live until the protected filesystem work ends.
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := s.lock.Heartbeat(context.WithoutCancel(ctx)); err != nil {
					err = fmt.Errorf("renew workspace bundle store lock: %w", err)
					cancel(err)
					heartbeatDone <- err
					return
				}
			}
		}
	}()

	defer func() {
		close(stopHeartbeat)
		if err := <-heartbeatDone; err != nil && !errors.Is(retErr, err) {
			retErr = errors.Join(retErr, err)
		}
		cancel(nil)
		if err := s.lock.Unlock(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("unlock workspace bundle store: %w", err))
		}
	}()
	return fn(lockCtx)
}

func (s *Store) ensureLock(ctx context.Context) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if !s.lock.IsHeldByMe() {
		return dirlock.ErrLockNotHeld
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := context.Cause(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func touch(path string) error {
	now := time.Now()
	return os.Chtimes(path, now, now)
}

func (s *Store) path(digest string) (string, error) {
	if !ValidDigest(digest) {
		return "", fmt.Errorf("invalid workspace bundle digest %q", digest)
	}
	return filepath.Join(s.dir, digest[:2], digest+archiveExt), nil
}
