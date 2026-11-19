// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package s3

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
)

// An unknown with key must fail validation instead of being silently dropped
// by the s3 executor (#3440). bucket/prefix remain accepted.
func TestS3RejectsUnknownWithKey(t *testing.T) {
	t.Parallel()

	err := registry.ValidateExecutorConfig("s3", map[string]any{
		"bucket": "my-bucket", "bukcet": "typo",
	})
	if err == nil {
		t.Fatal("unknown with key must be rejected")
	}
	if !strings.Contains(err.Error(), `did you mean "bucket" instead of "bukcet"?`) {
		t.Fatalf("expected closest-key hint, got %q", err)
	}

	if err := registry.ValidateExecutorConfig("s3", map[string]any{
		"bucket": "my-bucket", "key": "a.txt", "source": "local.txt", "force_path_style": true,
	}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}
