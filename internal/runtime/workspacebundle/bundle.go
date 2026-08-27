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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const (
	DefaultMaxCompressedSize   int64 = 64 << 20
	DefaultMaxUncompressedSize int64 = 256 << 20
	DefaultMaxFiles                  = 8192

	archiveExt  = ".tar.gz"
	metadataExt = ".json"

	storeLockStaleThreshold  = 30 * time.Second
	storeLockRetryInterval   = 50 * time.Millisecond
	storeLockHeartbeatPeriod = 10 * time.Second
)

var (
	errPathEscapesBundle = errors.New("path escapes workspace bundle")
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
	dir    string
	limits Limits
	lock   dirlock.DirLock
	mu     sync.Mutex
}

// Reference records one attempt's ownership of a workspace bundle.
type Reference struct {
	AttemptKey string `json:"attemptKey"`
	Digest     string `json:"digest"`
	RetainedAt int64  `json:"retainedAt"`
}

type managedBundle struct {
	Digest    string `json:"digest"`
	CreatedAt int64  `json:"createdAt"`
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
	return &Store{
		dir:    dir,
		limits: normalizeLimits(limits),
		lock: dirlock.New(dir, &dirlock.LockOptions{
			StaleThreshold: storeLockStaleThreshold,
			RetryInterval:  storeLockRetryInterval,
		}),
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

	return s.withLock(ctx, func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return s.markManaged(ctx, desc.Digest)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat workspace bundle: %w", err)
		}
		if err := fileutil.ReplaceFile(tmpName, path); err != nil {
			return fmt.Errorf("commit workspace bundle: %w", err)
		}
		cleanup = false
		if err := s.markManaged(ctx, desc.Digest); err != nil {
			_ = fileutil.Remove(path)
			return err
		}
		return nil
	})
}

// Retain associates an attempt with an existing workspace bundle.
func (s *Store) Retain(ctx context.Context, attemptKey, digest string) (bool, error) {
	attemptKey = strings.TrimSpace(attemptKey)
	if attemptKey == "" {
		return false, fmt.Errorf("%w: workspace bundle attempt key is required", os.ErrInvalid)
	}
	if !ValidDigest(digest) {
		return false, fmt.Errorf("%w: invalid workspace bundle digest %q", os.ErrInvalid, digest)
	}

	var retained bool
	err := s.withLock(ctx, func(ctx context.Context) error {
		bundlePath, err := s.path(digest)
		if err != nil {
			return err
		}
		if _, err := os.Stat(bundlePath); err != nil {
			return fmt.Errorf("stat workspace bundle: %w", err)
		}

		referencePath := s.referencePath(attemptKey)
		existing, err := readReference(referencePath)
		switch {
		case err == nil:
			if existing.AttemptKey != attemptKey {
				return fmt.Errorf("%w: workspace bundle reference key mismatch", os.ErrExist)
			}
			if existing.Digest != digest {
				return fmt.Errorf("%w: workspace bundle attempt %q already references %s", os.ErrExist, attemptKey, existing.Digest)
			}
			return nil
		case !os.IsNotExist(err):
			return err
		}

		if err := s.markManaged(ctx, digest); err != nil {
			return err
		}
		if err := writeJSONFile(ctx, referencePath, Reference{
			AttemptKey: attemptKey,
			Digest:     digest,
			RetainedAt: time.Now().UTC().UnixMilli(),
		}); err != nil {
			return err
		}
		retained = true
		return nil
	})
	return retained, err
}

// Release removes an attempt reference and its now-unreferenced bundle.
func (s *Store) Release(ctx context.Context, attemptKey string) error {
	attemptKey = strings.TrimSpace(attemptKey)
	if attemptKey == "" {
		return nil
	}

	return s.withLock(ctx, func(ctx context.Context) error {
		references, err := s.listReferences(ctx)
		if err != nil {
			return err
		}
		var released *Reference
		for i := range references {
			if references[i].AttemptKey == attemptKey {
				released = &references[i]
				break
			}
		}
		if released == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fileutil.Remove(s.referencePath(attemptKey)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove workspace bundle reference: %w", err)
		}
		for _, reference := range references {
			if reference.AttemptKey != attemptKey && reference.Digest == released.Digest {
				return nil
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.removeManagedBundle(released.Digest)
	})
}

// ListReferences returns all managed workspace bundle references.
func (s *Store) ListReferences(ctx context.Context) ([]Reference, error) {
	var references []Reference
	err := s.withLock(ctx, func(ctx context.Context) error {
		var err error
		references, err = s.listReferences(ctx)
		return err
	})
	return references, err
}

// CleanupUnreferenced removes old managed bundles without references.
func (s *Store) CleanupUnreferenced(ctx context.Context, before time.Time) (int, error) {
	var removed int
	err := s.withLock(ctx, func(ctx context.Context) error {
		references, err := s.listReferences(ctx)
		if err != nil {
			return err
		}
		retained := make(map[string]struct{}, len(references))
		for _, reference := range references {
			retained[reference.Digest] = struct{}{}
		}

		entries, err := os.ReadDir(filepath.Join(s.dir, "managed"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("list managed workspace bundles: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != metadataExt {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			metadataPath := filepath.Join(s.dir, "managed", entry.Name())
			var metadata managedBundle
			if err := readJSONFile(metadataPath, &metadata); err != nil {
				logger.Warn(ctx, "Skipping unreadable workspace bundle metadata",
					slog.String("path", metadataPath),
					tag.Error(err),
				)
				continue
			}
			if !ValidDigest(metadata.Digest) {
				logger.Warn(ctx, "Skipping workspace bundle metadata with invalid digest",
					slog.String("path", metadataPath),
					slog.String("digest", metadata.Digest),
				)
				continue
			}
			if entry.Name() != metadata.Digest+metadataExt || metadata.CreatedAt <= 0 {
				logger.Warn(ctx, "Skipping workspace bundle metadata with invalid identity",
					slog.String("path", metadataPath),
					slog.Int64("createdAt", metadata.CreatedAt),
				)
				continue
			}
			if _, ok := retained[metadata.Digest]; ok || !time.UnixMilli(metadata.CreatedAt).Before(before) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.removeManagedBundle(metadata.Digest); err != nil {
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

func (s *Store) withLock(ctx context.Context, fn func(context.Context) error) (retErr error) {
	if strings.TrimSpace(s.dir) == "" {
		return fmt.Errorf("workspace bundle store is not configured")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.lock.Lock(ctx); err != nil {
		return fmt.Errorf("lock workspace bundle store: %w", err)
	}

	// Keep the lease live while shared-storage scans hold the lock.
	heartbeatDone := make(chan error, 1)
	stopHeartbeat := make(chan struct{})
	go func() {
		ticker := time.NewTicker(storeLockHeartbeatPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				heartbeatDone <- nil
				return
			case <-stopHeartbeat:
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if err := s.lock.Heartbeat(ctx); err != nil {
					heartbeatDone <- fmt.Errorf("heartbeat workspace bundle store lock: %w", err)
					return
				}
			}
		}
	}()
	defer func() {
		close(stopHeartbeat)
		retErr = errors.Join(retErr, <-heartbeatDone)
		if err := s.lock.Unlock(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("unlock workspace bundle store: %w", err))
		}
	}()

	return fn(ctx)
}

func (s *Store) listReferences(ctx context.Context) ([]Reference, error) {
	root := filepath.Join(s.dir, "refs")
	var references []Reference
	err := filepath.WalkDir(root, func(referencePath string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != metadataExt {
			return nil
		}
		reference, err := readReference(referencePath)
		if err != nil {
			return err
		}
		if reference.AttemptKey == "" || !ValidDigest(reference.Digest) {
			return fmt.Errorf("invalid workspace bundle reference %q", referencePath)
		}
		references = append(references, *reference)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list workspace bundle references: %w", err)
	}
	sort.Slice(references, func(i, j int) bool {
		return references[i].AttemptKey < references[j].AttemptKey
	})
	return references, nil
}

func (s *Store) markManaged(ctx context.Context, digest string) error {
	metadataPath := s.managedPath(digest)
	if _, err := os.Stat(metadataPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat managed workspace bundle: %w", err)
	}
	return writeJSONFile(ctx, metadataPath, managedBundle{
		Digest:    digest,
		CreatedAt: time.Now().UTC().UnixMilli(),
	})
}

func (s *Store) removeManagedBundle(digest string) error {
	bundlePath, err := s.path(digest)
	if err != nil {
		return err
	}
	if err := fileutil.Remove(bundlePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove workspace bundle: %w", err)
	}
	if err := fileutil.Remove(s.managedPath(digest)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove managed workspace bundle metadata: %w", err)
	}
	return nil
}

func (s *Store) referencePath(attemptKey string) string {
	digest := sha256.Sum256([]byte(attemptKey))
	name := hex.EncodeToString(digest[:])
	return filepath.Join(s.dir, "refs", name[:2], name+metadataExt)
}

func (s *Store) managedPath(digest string) string {
	return filepath.Join(s.dir, "managed", digest+metadataExt)
}

func readReference(path string) (*Reference, error) {
	var reference Reference
	if err := readJSONFile(path, &reference); err != nil {
		return nil, err
	}
	return &reference, nil
}

func readJSONFile(path string, dest any) error {
	data, err := os.ReadFile(path) //nolint:gosec // Paths are derived from configured storage and hashed keys.
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("decode %q: %w", path, err)
	}
	return nil
}

func writeJSONFile(ctx context.Context, path string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create metadata directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".metadata-*.tmp")
	if err != nil {
		return fmt.Errorf("create metadata file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = fileutil.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write metadata file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close metadata file: %w", err)
	}
	if err := fileutil.ReplaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("commit metadata file: %w", err)
	}
	return nil
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
	size, err := io.Copy(hasher, io.LimitReader(file, s.limits.MaxCompressedSize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("read workspace bundle: %w", err)
	}
	if size > s.limits.MaxCompressedSize {
		return nil, 0, fmt.Errorf("workspace bundle exceeds compressed size limit %d", s.limits.MaxCompressedSize)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != digest {
		return nil, 0, fmt.Errorf("workspace bundle digest mismatch: got %s, want %s", actual, digest)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("rewind workspace bundle: %w", err)
	}
	closeOnError = false
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

func (s *Store) path(digest string) (string, error) {
	if !ValidDigest(digest) {
		return "", fmt.Errorf("invalid workspace bundle digest %q", digest)
	}
	return filepath.Join(s.dir, digest[:2], digest+archiveExt), nil
}
