// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The artifact-relative path rule is implemented twice: here for values
// resolved at run time, and as cleanStepArtifactPath in internal/spec for
// authored values. Human-task artifact safety depends on the two agreeing, so
// this table is kept byte-identical with TestCleanStepArtifactPathAgreement
// apart from the empty-path message, which is the one intended divergence.
func TestCleanArtifactOutputPathAgreement(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "Empty", input: "", wantErr: "artifact path must not be empty"},
		{name: "WhitespaceOnly", input: "   ", wantErr: "artifact path must not be empty"},
		{name: "Absolute", input: "/etc/passwd", wantErr: "artifact path must be relative"},
		{name: "Home", input: "~", wantErr: "artifact path must be relative"},
		{name: "HomeRelative", input: "~/secret", wantErr: "artifact path must be relative"},
		{name: "WindowsDrive", input: "C:/secret", wantErr: "artifact path must be relative"},
		{name: "LeadingParent", input: "../secret", wantErr: "artifact path must not contain parent directory segments"},
		{name: "InteriorParent", input: "a/../b", wantErr: "artifact path must not contain parent directory segments"},
		{name: "CurrentDirectory", input: ".", wantErr: "artifact path must name a file"},
		{name: "Backslashes", input: `reports\test.html`, want: "reports/test.html"},
		{name: "TrailingSlash", input: "reports/", want: "reports"},
		{name: "DoubleSeparator", input: "a//b.txt", want: "a/b.txt"},
		{name: "LeadingDotSlash", input: "./a.txt", want: "a.txt"},
		{name: "SurroundingSpace", input: "  a.txt  ", want: "a.txt"},
		{name: "Nested", input: "reports/2026/a.txt", want: "reports/2026/a.txt"},
		{name: "UnresolvedReference", input: "${params.OUT}/report.html", want: "${params.OUT}/report.html"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cleanArtifactOutputPath(tt.input)

			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
