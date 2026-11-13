// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package archive

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
)

// An unknown with key must fail validation instead of being silently dropped
// by the archive executor (#3440). source/destination remain accepted.
func TestArchiveRejectsUnknownWithKey(t *testing.T) {
	t.Parallel()

	err := registry.ValidateExecutorConfig("archive", map[string]any{
		"source": "a.txt", "destinaton": "out.zip",
	})
	if err == nil {
		t.Fatal("unknown with key must be rejected")
	}
	if !strings.Contains(err.Error(), `did you mean "destination" instead of "destinaton"?`) {
		t.Fatalf("expected closest-key hint, got %q", err)
	}

	if err := registry.ValidateExecutorConfig("archive", map[string]any{
		"source": "a.txt", "destination": "out.zip", "dry_run": true,
	}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
