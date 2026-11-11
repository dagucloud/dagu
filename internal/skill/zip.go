// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// zipEpoch is the time stamp of every entry of a ZIP, so that the same skills
// give the same bytes.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// ErrDownloadTooLarge is returned when the files of the skills of a ZIP are
// above MaxDownloadBytes.
var ErrDownloadTooLarge = errors.New("the skills are too large for one ZIP")

// DownloadSkills reads the skills that the names give, without file content.
// It fails with ErrNotFound for the first name that does not exist, so that the
// caller sends no partial ZIP. The result is sorted by name, without duplicates.
// It fails with ErrDownloadTooLarge when the files are too many or too large.
func (s *Store) DownloadSkills(names []string) ([]*Skill, error) {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		unique = append(unique, n)
	}
	sort.Strings(unique)
	if len(unique) > MaxDownloadSkills {
		return nil, fmt.Errorf("%w: %d skills asked, the limit is %d", ErrDownloadTooLarge, len(unique), MaxDownloadSkills)
	}
	var total int64
	out := make([]*Skill, 0, len(unique))
	for _, n := range unique {
		sk, err := s.Read(n)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, n)
			}
			return nil, err
		}
		for _, f := range sk.Files {
			total += f.Size
		}
		if total > MaxDownloadBytes {
			return nil, fmt.Errorf("%w: more than %d bytes", ErrDownloadTooLarge, MaxDownloadBytes)
		}
		out = append(out, sk)
	}
	return out, nil
}

// WriteZip writes the skills as one ZIP. Each skill is a top-level folder
// <name>/ that holds its files, exactly as stored. The order of the entries is
// the order of the skills, then the order of the paths. Every entry has the
// same time stamp and the mode 0644, so the same skills give the same bytes.
// A skill that is not valid is written too. A file that changed since the read
// of the skill stops the write with an error.
func WriteZip(w io.Writer, skills []*Skill) error {
	zw := zip.NewWriter(w)
	for _, sk := range skills {
		for _, f := range sk.Files {
			data, err := sk.ReadVerified(f.Path)
			if err != nil {
				return err
			}
			header := &zip.FileHeader{
				Name:     sk.Name + "/" + f.Path,
				Method:   zip.Deflate,
				Modified: zipEpoch,
			}
			header.SetMode(0o644)
			fw, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			if _, err := fw.Write(data); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

// ZipFileName returns the file name of the ZIP of the skills: <name>.zip for
// one skill and skills-<date>.zip for several.
func ZipFileName(names []string, now time.Time) string {
	if len(names) == 1 && IsSafeFolderName(names[0]) && isASCII(names[0]) && !strings.ContainsAny(names[0], `"; `) {
		return names[0] + ".zip"
	}
	if len(names) == 1 {
		return "skill.zip"
	}
	return "skills-" + now.UTC().Format("2006-01-02") + ".zip"
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
