// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
)

type checksumFileEntry struct {
	ID        string `json:"id"`
	Checksum  string `json:"checksum"`
	Algorithm string `json:"algorithm"`
}

type checksumFileContent struct {
	Checksums []checksumFileEntry `json:"checksums"`
}

// verifyPackageDigests compares declared package digests against the artifact
// checksums aqua recorded during the install. Packages without a digest are
// skipped.
func verifyPackageDigests(checksumFile string, packages []core.ToolPackage) error {
	declared := 0
	for _, pkg := range packages {
		if strings.TrimSpace(pkg.Digest) != "" {
			declared++
		}
	}
	if declared == 0 {
		return nil
	}

	data, err := os.ReadFile(checksumFile) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read aqua checksum file for digest verification: %w", err)
	}
	var content checksumFileContent
	if err := json.Unmarshal(data, &content); err != nil {
		return fmt.Errorf("parse aqua checksum file for digest verification: %w", err)
	}

	for _, pkg := range packages {
		digest := strings.TrimSpace(pkg.Digest)
		if digest == "" {
			continue
		}
		if err := verifyPackageDigest(content.Checksums, pkg, digest); err != nil {
			return err
		}
	}
	return nil
}

func verifyPackageDigest(entries []checksumFileEntry, pkg core.ToolPackage, digest string) error {
	want, _ := strings.CutPrefix(digest, "sha256:")
	marker := "/" + pkg.Package + "/" + pkg.Version + "/"
	matched := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.ID, "registries/") || !strings.Contains(entry.ID, marker) {
			continue
		}
		matched++
		if !strings.EqualFold(strings.TrimSpace(entry.Algorithm), "sha256") {
			return fmt.Errorf("digest verification for %s@%s: recorded checksum uses algorithm %q, expected sha256",
				pkg.Package, pkg.Version, entry.Algorithm)
		}
		if !strings.EqualFold(strings.TrimSpace(entry.Checksum), want) {
			return fmt.Errorf("digest mismatch for %s@%s: declared sha256:%s, downloaded artifact is sha256:%s (%s)",
				pkg.Package, pkg.Version, want, strings.ToLower(strings.TrimSpace(entry.Checksum)), entry.ID)
		}
	}
	if matched == 0 {
		return fmt.Errorf("digest verification for %s@%s: no recorded artifact checksum matched; digest pinning requires a package type whose artifact checksum aqua records (e.g. github_release)",
			pkg.Package, pkg.Version)
	}
	return nil
}
