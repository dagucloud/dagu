// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An explicitly empty --worker-label is refused rather than skipped, so a
// routing request can never silently leave the DAG's own selector in place.
func TestWorkerLabelsFromFlags(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want map[string]string
		err  string
	}{
		"none":          {args: nil, want: map[string]string{}},
		"pair":          {args: []string{"--worker-label", "zone=b"}, want: map[string]string{"zone": "b"}},
		"two":           {args: []string{"--worker-label", "zone=b", "--worker-label=gpu=yes"}, want: map[string]string{"zone": "b", "gpu": "yes"}},
		"empty":         {args: []string{"--worker-label", ""}, err: `invalid worker label ""`},
		"blank":         {args: []string{"--worker-label", "  "}, err: `invalid worker label "  "`},
		"no value":      {args: []string{"--worker-label", "zone="}, err: "expected key=value"},
		"no key":        {args: []string{"--worker-label", "=b"}, err: "expected key=value"},
		"bad charset":   {args: []string{"--worker-label", "zone=b c"}, err: "invalid characters"},
		"empty amid ok": {args: []string{"--worker-label", "zone=b", "--worker-label", ""}, err: `invalid worker label ""`},
	} {
		t.Run(name, func(t *testing.T) {
			command := Enqueue()
			require.NoError(t, command.Flags().Parse(tc.args))

			got, err := workerLabelsFromFlags(&Context{Command: command})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
