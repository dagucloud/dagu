// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// zipEpoch is the time stamp of every entry of a ZIP, so that the same skills
// give the same bytes.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// ErrDownloadRefused is returned when the skills cannot form one ZIP: too many
// skills, too many bytes, or names that a ZIP cannot hold side by side.
var ErrDownloadRefused = errors.New("the skills cannot be downloaded as one ZIP")

// DownloadSkills reads the skills that the names give, sorted by name and
// without duplicates, and checks the limits of one ZIP. It fails with
// ErrNotFound for the first name that does not exist, and with
// ErrDownloadRefused when the skills break a limit, so that the caller sends
// no partial ZIP. The result holds no file content.
func (s *Store) DownloadSkills(names []string) ([]*Skill, error) {
	unique := slices.Compact(slices.Sorted(slices.Values(names)))
	if len(unique) > MaxDownloadSkills {
		return nil, fmt.Errorf("%w: %d skills asked, the limit is %d", ErrDownloadRefused, len(unique), MaxDownloadSkills)
	}
	for i, n := range unique {
		// A careless unzip on Windows reads "c:" at the start of a path as a drive.
		if strings.Contains(n, ":") {
			return nil, fmt.Errorf("%w: the name %q has a colon", ErrDownloadRefused, n)
		}
		// On a file system that ignores case, the two folders of the ZIP are one.
		for _, other := range unique[:i] {
			if strings.EqualFold(n, other) {
				return nil, fmt.Errorf("%w: the names %q and %q differ only by case", ErrDownloadRefused, other, n)
			}
		}
	}
	var total int64
	out := make([]*Skill, 0, len(unique))
	for _, n := range unique {
		sk, err := s.Read(n)
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, n)
		}
		if err != nil {
			return nil, err
		}
		for _, f := range zipFiles(sk) {
			total += f.Size
		}
		if total > MaxDownloadBytes {
			return nil, fmt.Errorf("%w: the files are above %d bytes", ErrDownloadRefused, MaxDownloadBytes)
		}
		out = append(out, sk)
	}
	return out, nil
}

// WriteZip writes the skills as one ZIP. Each skill is a top-level folder
// <name>/ that holds its files, exactly as stored, in the order of the skills
// and then of the paths. Every entry has the same time stamp and the mode
// 0644, so the same skills give the same bytes. A skill that is not valid is
// written too, without a hidden file, a file with hard links or a file above
// MaxFileBytes.
//
// WriteZip reads one file at a time into memory, and writes it only when it
// has the SHA-256 that the read of the skill recorded. A file that changed,
// and the end of ctx, stop the write with an error before the end of the ZIP,
// so the output is not a valid ZIP.
func WriteZip(ctx context.Context, w io.Writer, skills []*Skill) error {
	zw := zip.NewWriter(w)
	var buf []byte
	for _, sk := range skills {
		for _, f := range zipFiles(sk) {
			if err := ctx.Err(); err != nil {
				return err
			}
			buf = slices.Grow(buf[:0], int(f.Size))[:f.Size]
			if err := sk.readInto(f.Path, buf); err != nil {
				return err
			}
			header := &zip.FileHeader{Name: sk.Name + "/" + f.Path, Method: zip.Deflate, Modified: zipEpoch}
			header.SetMode(0o644)
			fw, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			if _, err := fw.Write(buf); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

// zipFiles lists the files of a skill that a ZIP holds. A hidden file other
// than .gitkeep, a file with hard links, which can be a file outside the
// folder, and a file above MaxFileBytes, which has no recorded SHA-256, stay
// out.
func zipFiles(sk *Skill) []File {
	linked := map[string]bool{}
	for _, issue := range sk.Issues {
		if issue.Rule == RuleHardlink {
			linked[issue.File] = true
		}
	}
	out := make([]File, 0, len(sk.Files))
	for _, f := range sk.Files {
		if f.Size <= MaxFileBytes && !linked[f.Path] && !isHiddenPath(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

// isHiddenPath reports whether a part of the path starts with a dot, except
// a last part .gitkeep.
func isHiddenPath(rel string) bool {
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ".") && (i < len(parts)-1 || part != KeepFileName) {
			return true
		}
	}
	return false
}
