// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dagucloud/dagu/internal/tools"
)

func writeManifest(path string, manifest *tools.Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tools manifest: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write tools manifest: %w", err)
	}
	return nil
}
