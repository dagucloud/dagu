// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/mholt/archives"
)

type extractionResult struct {
	Operation       string   `json:"operation"`
	Source          string   `json:"source"`
	Destination     string   `json:"destination"`
	FilesExtracted  int64    `json:"filesExtracted"`
	BytesExtracted  int64    `json:"bytesExtracted"`
	FilesSkipped    int64    `json:"filesSkipped"`
	Duration        string   `json:"duration"`
	Errors          []string `json:"errors,omitempty"`
	VerifyPerformed bool     `json:"verifyPerformed"`
}

type creationResult struct {
	Operation       string   `json:"operation"`
	Source          string   `json:"source"`
	Destination     string   `json:"destination"`
	FilesAdded      int64    `json:"filesAdded"`
	BytesArchived   int64    `json:"bytesArchived"`
	Duration        string   `json:"duration"`
	Errors          []string `json:"errors,omitempty"`
	VerifyPerformed bool     `json:"verifyPerformed"`
}

type listEntry struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"modTime"`
	IsDir   bool      `json:"isDir"`
}

type listResult struct {
	Operation   string      `json:"operation"`
	Source      string      `json:"source"`
	TotalFiles  int         `json:"totalFiles"`
	TotalSize   int64       `json:"totalSize"`
	Entries     []listEntry `json:"files"`
	Verified    bool        `json:"verified"`
	ElapsedTime string      `json:"duration"`
}

func (e *executorImpl) runExtract(ctx context.Context) error {
	start := time.Now()
	res := extractionResult{
		Operation:   opExtract,
		Source:      e.cfg.Source,
		Destination: e.cfg.Destination,
	}

	sourceInfo, err := os.Stat(e.cfg.Source)
	if err != nil {
		return wrapError(ErrSourceNotFound, err)
	}
	if sourceInfo.IsDir() {
		return wrapError(ErrConfig, fmt.Errorf("source must be a file"))
	}

	srcFile, err := os.Open(e.cfg.Source)
	if err != nil {
		return wrapError(ErrSourceNotFound, err)
	}
	defer srcFile.Close()

	format, err := e.determineFormat(ctx, e.cfg.Source, srcFile)
	if err != nil {
		return err
	}

	extractor, ok := format.(archives.Extractor)
	if !ok {
		return wrapError(ErrFormatDetection, fmt.Errorf("format does not support extraction"))
	}

	if e.cfg.VerifyIntegrity {
		if err := e.verifyArchiveReadable(ctx, e.cfg.Source, srcFile, format); err != nil {
			return wrapError(ErrCorrupted, err)
		}
		res.VerifyPerformed = true
		if err := resetReader(srcFile); err != nil {
			return wrapError(ErrExtract, err)
		}
	}

	dest := e.cfg.Destination
	if dest == "" {
		dest = "."
	}
	destRoot := filepath.Clean(dest)
	if err := e.ensureDir(dest); err != nil {
		return err
	}

	preservePaths := e.cfg.PreservePaths
	handle := func(handleCtx context.Context, file archives.FileInfo) error {
		select {
		case <-handleCtx.Done():
			return handleCtx.Err()
		default:
		}

		rel := sanitizeArchivePath(file.NameInArchive, e.cfg.StripComponents, preservePaths)
		if rel == "" {
			if file.IsDir() {
				return nil
			}
			e.warn("skipping unnamed file in archive")
			res.FilesSkipped++
			return nil
		}

		slashed := filepath.ToSlash(rel)
		if !matchesFilters(slashed, e.cfg.Include, e.cfg.Exclude) {
			res.FilesSkipped++
			return nil
		}

		targetPath, joinErr := e.secureJoin(dest, rel)
		if joinErr != nil {
			return joinErr
		}

		if file.IsDir() {
			if err := e.ensureDir(targetPath); err != nil {
				return err
			}
			return nil
		}

		parentDir := filepath.Dir(targetPath)
		if parentDir != "" && parentDir != "." {
			if err := e.ensureDir(parentDir); err != nil {
				return err
			}
		}

		if e.cfg.DryRun {
			res.FilesExtracted++
			res.BytesExtracted += file.Size()
			return nil
		}

		if !e.cfg.Overwrite {
			if _, err := os.Stat(targetPath); err == nil {
				return fmt.Errorf("%w: destination %q exists (overwrite disabled)", ErrDestination, targetPath)
			}
		}

		if file.Mode()&os.ModeSymlink != 0 {
			linkTarget := file.LinkTarget
			if linkTarget == "" {
				return fmt.Errorf("%w: empty symlink target", ErrCorrupted)
			}
			if filepath.IsAbs(linkTarget) {
				return fmt.Errorf("%w: symlink target %q is absolute", ErrCorrupted, linkTarget)
			}

			// Validate symlink doesn't escape destination directory
			symlinkParent := filepath.Dir(targetPath)
			resolvedTarget := filepath.Clean(filepath.Join(symlinkParent, linkTarget))
			if !pathWithin(destRoot, resolvedTarget) {
				return fmt.Errorf("%w: symlink target %q escapes destination", ErrCorrupted, linkTarget)
			}

			// Convert to safe relative path
			safeTarget, err := filepath.Rel(symlinkParent, resolvedTarget)
			if err != nil {
				return wrapError(ErrExtract, err)
			}

			_ = fileutil.Remove(targetPath)
			if err := os.Symlink(safeTarget, targetPath); err != nil {
				if errors.Is(err, fs.ErrPermission) {
					return fmt.Errorf("%w: %v", ErrPermission, err)
				}
				return wrapError(ErrDestination, err)
			}
			res.FilesExtracted++
			return nil
		}

		destFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileModeFor(file))
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return fmt.Errorf("%w: %v", ErrPermission, err)
			}
			return wrapError(ErrDestination, err)
		}
		defer destFile.Close()

		src, err := file.Open()
		if err != nil {
			return err
		}
		defer src.Close()

		written, err := io.Copy(destFile, src)
		if err != nil {
			return wrapError(ErrExtract, err)
		}

		res.FilesExtracted++
		res.BytesExtracted += written
		return nil
	}

	err = extractor.Extract(ctx, srcFile, func(hctx context.Context, info archives.FileInfo) error {
		if err := handle(hctx, info); err != nil {
			if e.cfg.ContinueOnError {
				res.Errors = append(res.Errors, err.Error())
				e.warn("continuing after error: %v", err)
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil {
		return wrapError(ErrExtract, err)
	}

	res.Duration = time.Since(start).String()
	return e.resultWriter(res)
}

func (e *executorImpl) runCreate(ctx context.Context) error {
	start := time.Now()
	res := creationResult{
		Operation:   opCreate,
		Source:      e.cfg.Source,
		Destination: e.cfg.Destination,
	}

	srcInfo, err := os.Stat(e.cfg.Source)
	if err != nil {
		return wrapError(ErrSourceNotFound, err)
	}

	if !e.cfg.DryRun {
		destDir := filepath.Dir(e.cfg.Destination)
		if destDir != "" {
			if err := e.ensureDir(destDir); err != nil {
				return err
			}
		}
	}

	format, err := e.archiveFormatForCreate()
	if err != nil {
		return err
	}

	filesMap := map[string]string{
		e.cfg.Source: "",
	}

	if srcInfo.IsDir() {
		filesMap[e.cfg.Source] = filepath.Base(e.cfg.Source)
	}

	files, err := archives.FilesFromDisk(ctx, &archives.FromDiskOptions{
		FollowSymlinks: e.cfg.FollowSymlinks,
	}, filesMap)
	if err != nil {
		return wrapError(ErrCreate, err)
	}

	filtered := make([]archives.FileInfo, 0, len(files))
	for _, f := range files {
		pathInArchive := filepath.ToSlash(f.NameInArchive)
		if pathInArchive == "" {
			pathInArchive = filepath.Base(f.Name())
			f.NameInArchive = pathInArchive
		}
		if !matchesFilters(pathInArchive, e.cfg.Include, e.cfg.Exclude) {
			continue
		}
		filtered = append(filtered, f)
	}

	if e.cfg.DryRun {
		for _, f := range filtered {
			res.FilesAdded++
			res.BytesArchived += f.Size()
		}
		res.Duration = time.Since(start).String()
		return e.resultWriter(res)
	}

	outFile, err := os.Create(e.cfg.Destination)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("%w: %v", ErrPermission, err)
		}
		return wrapError(ErrDestination, err)
	}
	defer outFile.Close()

	archiver, ok := format.(archives.Archiver)
	if !ok {
		return wrapError(ErrCreate, fmt.Errorf("format %T cannot create archives", format))
	}

	if err := archiver.Archive(ctx, outFile, filtered); err != nil {
		return wrapError(ErrCreate, err)
	}

	for _, f := range filtered {
		res.FilesAdded++
		res.BytesArchived += f.Size()
	}

	if e.cfg.VerifyIntegrity {
		if err := e.verifyArchiveReadable(ctx, e.cfg.Destination, nil, format); err != nil {
			return wrapError(ErrCorrupted, err)
		}
		res.VerifyPerformed = true
	}

	res.Duration = time.Since(start).String()
	return e.resultWriter(res)
}

func (e *executorImpl) runList(ctx context.Context) error {
	start := time.Now()
	res := listResult{
		Operation: opList,
		Source:    e.cfg.Source,
	}

	file, err := os.Open(e.cfg.Source)
	if err != nil {
		return wrapError(ErrSourceNotFound, err)
	}
	defer file.Close()

	format, err := e.determineFormat(ctx, e.cfg.Source, file)
	if err != nil {
		return err
	}

	if e.cfg.VerifyIntegrity {
		if err := e.verifyArchiveReadable(ctx, e.cfg.Source, file, format); err != nil {
			return wrapError(ErrCorrupted, err)
		}
		res.Verified = true
		if err := resetReader(file); err != nil {
			return wrapError(ErrCorrupted, err)
		}
	}

	entries, err := e.walkArchive(ctx, file, e.cfg.Source, format)
	if err != nil {
		return err
	}

	filtered := make([]listEntry, 0, len(entries))
	var filteredSize int64
	for _, entry := range entries {
		if !matchesFilters(filepath.ToSlash(entry.Path), e.cfg.Include, e.cfg.Exclude) {
			continue
		}
		filtered = append(filtered, entry)
		if !entry.IsDir {
			filteredSize += entry.Size
		}
	}

	res.Entries = filtered
	res.TotalFiles = len(filtered)
	res.TotalSize = filteredSize
	res.ElapsedTime = time.Since(start).String()
	return e.resultWriter(res)
}

func (e *executorImpl) determineFormat(ctx context.Context, path string, reader archives.ReaderAtSeeker) (archives.Format, error) {
	if cfgFmt := strings.TrimSpace(e.cfg.Format); cfgFmt != "" {
		format, err := formatFromString(cfgFmt, e.cfg.CompressionLevel)
		if err != nil {
			return nil, wrapError(ErrFormatDetection, err)
		}
		return applyPassword(format, e.cfg.Password), nil
	}

	if reader == nil {
		file, err := os.Open(path)
		if err != nil {
			return nil, wrapError(ErrSourceNotFound, err)
		}
		defer file.Close()
		reader = file
	}

	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil, wrapError(ErrFormatDetection, err)
	}

	format, _, err := archives.Identify(ctx, filepath.Base(path), reader)
	if err != nil {
		return nil, wrapError(ErrFormatDetection, err)
	}
	return applyPassword(format, e.cfg.Password), nil
}

func applyPassword(format archives.Format, password string) archives.Format {
	if password == "" {
		return format
	}

	switch f := format.(type) {
	case archives.SevenZip:
		f.Password = password
		return f
	case *archives.SevenZip:
		f.Password = password
		return f
	case archives.Rar:
		f.Password = password
		return f
	case *archives.Rar:
		f.Password = password
		return f
	}
	return format
}

func (e *executorImpl) archiveFormatForCreate() (archives.Format, error) {
	formatName := strings.TrimSpace(e.cfg.Format)
	if formatName == "" {
		formatName = extensionToFormat(e.cfg.Destination)
	}
	if formatName == "" {
		return nil, wrapError(ErrFormatDetection, fmt.Errorf("could not infer format; specify config.format"))
	}
	return formatFromString(formatName, e.cfg.CompressionLevel)
}

func (e *executorImpl) verifyArchiveReadable(ctx context.Context, path string, stream archives.ReaderAtSeeker, format archives.Format) error {
	var (
		file       *os.File
		needsClose bool
		err        error
		reader     archives.ReaderAtSeeker = stream
	)

	if reader == nil {
		file, err = os.Open(path)
		if err != nil {
			return err
		}
		reader = file
		needsClose = true
	}

	defer func() {
		if needsClose {
			_ = file.Close()
		}
	}()

	if format == nil {
		format, err = e.determineFormat(ctx, path, reader)
		if err != nil {
			return err
		}
		if err := resetReader(reader); err != nil {
			return err
		}
	}

	fsys, err := e.openArchiveFS(ctx, path, reader, format)
	if err != nil {
		return err
	}

	return fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		f, err := fsys.Open(rel)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(io.Discard, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func (e *executorImpl) walkArchive(ctx context.Context, file *os.File, path string, format archives.Format) ([]listEntry, error) {
	var (
		reader     archives.ReaderAtSeeker = file
		needsClose bool
	)

	if reader == nil {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return nil, wrapError(ErrSourceNotFound, err)
		}
		reader = file
		needsClose = true
	}

	if needsClose {
		defer file.Close()
	}

	if err := resetReader(reader); err != nil {
		return nil, wrapError(ErrCorrupted, err)
	}

	fsys, err := e.openArchiveFS(ctx, path, reader, format)
	if err != nil {
		return nil, wrapError(ErrCorrupted, err)
	}

	var entries []listEntry
	if walkErr := fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, listEntry{
			Path:    filepath.ToSlash(rel),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime(),
			IsDir:   d.IsDir(),
		})
		return nil
	}); walkErr != nil {
		return nil, wrapError(ErrCorrupted, walkErr)
	}

	return entries, nil
}

func (e *executorImpl) openArchiveFS(ctx context.Context, path string, stream archives.ReaderAtSeeker, format archives.Format) (fs.FS, error) {
	if format != nil {
		if extraction, ok := format.(archives.Extraction); ok {
			af := &archives.ArchiveFS{
				Format:  extraction,
				Context: ctx,
				Path:    path,
			}
			if stream != nil {
				section, err := newSectionReader(stream)
				if err != nil {
					return nil, err
				}
				af.Stream = section
			}
			return af, nil
		}
	}
	return archives.FileSystem(ctx, path, stream)
}

func extensionToFormat(path string) string {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".tar.gz"), strings.HasSuffix(name, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(name, ".tar.bz2"), strings.HasSuffix(name, ".tbz2"), strings.HasSuffix(name, ".tbz"):
		return "tar.bz2"
	case strings.HasSuffix(name, ".tar.xz"), strings.HasSuffix(name, ".txz"):
		return "tar.xz"
	case strings.HasSuffix(name, ".tar.zst"), strings.HasSuffix(name, ".tar.zstd"):
		return "tar.zst"
	case strings.HasSuffix(name, ".tar"):
		return "tar"
	case strings.HasSuffix(name, ".zip"):
		return "zip"
	case strings.HasSuffix(name, ".gz"):
		return "gz"
	case strings.HasSuffix(name, ".bz2"):
		return "bz2"
	case strings.HasSuffix(name, ".xz"):
		return "xz"
	case strings.HasSuffix(name, ".zst"), strings.HasSuffix(name, ".zstd"):
		return "zst"
	case strings.HasSuffix(name, ".lz4"):
		return "lz4"
	default:
		return ""
	}
}

func formatFromString(name string, compressionLevel int) (archives.Format, error) {
	switch strings.ToLower(name) {
	case "zip":
		return archives.Zip{}, nil
	case "tar":
		return archives.Tar{}, nil
	case "tar.gz", "tgz":
		return archives.CompressedArchive{
			Archival:    archives.Tar{},
			Extraction:  archives.Tar{},
			Compression: gzWithLevel(compressionLevel),
		}, nil
	case "tar.bz2", "tbz2", "tbz":
		return archives.CompressedArchive{
			Archival:    archives.Tar{},
			Extraction:  archives.Tar{},
			Compression: bz2WithLevel(compressionLevel),
		}, nil
	case "tar.xz", "txz":
		return archives.CompressedArchive{
			Archival:    archives.Tar{},
			Extraction:  archives.Tar{},
			Compression: archives.Xz{},
		}, nil
	case "tar.zst", "tar.zstd":
		return archives.CompressedArchive{
			Archival:    archives.Tar{},
			Extraction:  archives.Tar{},
			Compression: archives.Zstd{},
		}, nil
	case "gz":
		return gzWithLevel(compressionLevel), nil
	case "bz2":
		return bz2WithLevel(compressionLevel), nil
	case "xz":
		return archives.Xz{}, nil
	case "zst", "zstd":
		return archives.Zstd{}, nil
	case "lz4":
		return archives.Lz4{}, nil
	case "7z":
		return archives.SevenZip{}, nil
	case "rar":
		return archives.Rar{}, nil
	default:
		return nil, fmt.Errorf("unknown format %q", name)
	}
}

func gzWithLevel(level int) archives.Gz {
	if level < 0 {
		return archives.Gz{}
	}
	return archives.Gz{CompressionLevel: level}
}

func bz2WithLevel(level int) archives.Bz2 {
	if level < 0 {
		return archives.Bz2{}
	}
	return archives.Bz2{CompressionLevel: level}
}

func fileModeFor(info archives.FileInfo) fs.FileMode {
	mode := info.Mode()
	if mode == 0 {
		return 0o644
	}
	return mode
}

func resetReader(r archives.ReaderAtSeeker) error {
	if r == nil {
		return nil
	}
	_, err := r.Seek(0, io.SeekStart)
	return err
}

func newSectionReader(r archives.ReaderAtSeeker) (*io.SectionReader, error) {
	if r == nil {
		return nil, fmt.Errorf("nil reader")
	}
	current, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if _, err := r.Seek(current, io.SeekStart); err != nil {
		return nil, err
	}
	return io.NewSectionReader(r, 0, size), nil
}
