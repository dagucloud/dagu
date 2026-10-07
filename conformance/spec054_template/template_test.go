// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec054_template_test covers template action wiring through the CLI.
package spec054_template_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestTemplateRender(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("start", "render.yaml").ExpectExitCode(0)
	dagu.ExpectFileContent("inline.txt", "literal: ${FOO}, rendered: bar")
	dagu.ExpectFileContent("reference.txt", "Hi Ref")
	dagu.ExpectFileContent("nested/dir/rendered.txt", "Hello, File!")
}

func TestTemplateErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ command, file, message string }{
		{"validate", "missing_both.yaml", "requires exactly one of with.template or with.template_ref"},
		{"validate", "both_set.yaml", "requires exactly one of with.template or with.template_ref"},
		{"validate", "bad_template_ref.yaml", "must be one complete scoped value reference"},
		{"start", "missingkey.yaml", "map has no entry for key"},
		{"validate", "parse_error.yaml", "template: parse error"},
		{"validate", "missing_dot.yaml", `"report" is a with.data key and needs a leading dot`},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run(tc.command, tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}
