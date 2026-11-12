// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec093_ai_endpoints_test

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// Spec 093, "Storage" and "Errors": the refs under `ai/` belong to the AI feature.
// A DAG block `secrets` cannot name one, so a DAG author cannot read the key of an
// endpoint or the credential of an MCP entry. `dagu validate` and `dagu start`
// both refuse such a DAG and name the ref. A ref outside the prefix is valid.
func TestSecretsBlockRefusesReservedRefs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture string
		ref     string
	}{
		{"invalid_secret_reserved_endpoint.yaml", "ai/endpoint/test-endpoint"},
		{"invalid_secret_reserved_mcp.yaml", "ai/mcp/search/authorization"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(`registry ref "`+tc.ref+`" is reserved for the AI feature`, `"ai/"`)

			env := []string{
				"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"),
				"DAGU_COORDINATOR_ENABLED=false",
			}
			started := dagu.RunWithEnv(env, "start", tc.fixture)
			started.ExpectNonZeroExitCode()
			started.ExpectStderrContains(`registry ref "` + tc.ref + `" is reserved for the AI feature`)
		})
	}

	dagu := harness.NewRunner(t)
	dagu.Run("validate", "secret_registry_ref.yaml").ExpectExitCode(0)
}
