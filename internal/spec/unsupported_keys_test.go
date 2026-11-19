// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

// A step key whose runtime this version lacks fails the load, so that no run
// ignores it. The refusal also shows when another rule of the step fails.
func TestUnsupportedStepKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "secrets",
			yaml: `
secrets:
  - name: TOKEN
    provider: env
    key: SRC_TOKEN
steps:
  - name: push
    secrets: [TOKEN]
    run: echo hi
`,
			want: "the step key secrets is not supported in this version of Dagu",
		},
		{
			name: "untrusted",
			yaml: `
steps:
  - name: verify
    untrusted: true
    run: ./test.sh
`,
			want: "the step key untrusted is not supported in this version of Dagu",
		},
		{
			name: "untrusted false",
			yaml: `
steps:
  - name: verify
    untrusted: false
    run: ./test.sh
`,
			want: "the step key untrusted is not supported in this version of Dagu",
		},
		{
			name: "secrets on a handler",
			yaml: `
secrets:
  - name: TOKEN
    provider: env
    key: SRC_TOKEN
handler_on:
  exit:
    secrets: [TOKEN]
    run: echo bye
steps:
  - run: echo hi
`,
			want: "the step key secrets is not supported in this version of Dagu",
		},
		{
			name: "agent.run",
			yaml: `
steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: Fix the tests.
      endpoint:
        name: claude-main
        model: claude-sonnet-4-5
`,
			want: "the action agent.run is not supported in this version of Dagu",
		},
		{
			name: "agent.run with another error",
			yaml: `
steps:
  - name: code
    action: agent.run
    with:
      provider: claude
      prompt: Fix the tests.
`,
			want: "the action agent.run is not supported in this version of Dagu",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(t.Context(), []byte(tc.yaml))
			require.ErrorContains(t, err, tc.want)
		})
	}
}
