// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRestartPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    RestartPolicy
		enabled bool
		wantErr bool
	}{
		{name: "empty is no", input: "", want: RestartPolicyNo},
		{name: "no", input: "no", want: RestartPolicyNo},
		{name: "uppercase NO", input: "NO", want: RestartPolicyNo},
		{name: "always", input: "always", want: RestartPolicyAlways, enabled: true},
		{name: "uppercase Always", input: "Always", want: RestartPolicyAlways, enabled: true},
		{name: "unless-stopped", input: "unless-stopped", want: RestartPolicyUnlessStopped, enabled: true},
		{name: "whitespace padded", input: "  unless-stopped  ", want: RestartPolicyUnlessStopped, enabled: true},
		{name: "on-failure is not supported", input: "on-failure", wantErr: true},
		{name: "underscore spelling", input: "unless_stopped", wantErr: true},
		{name: "invalid value", input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRestartPolicy(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.enabled, got.Enabled())
		})
	}
}

func TestParseLoopOnFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    LoopOnFailure
		wantErr bool
	}{
		{name: "empty is restart", input: "", want: LoopOnFailureRestart},
		{name: "restart", input: "restart", want: LoopOnFailureRestart},
		{name: "uppercase Restart", input: "Restart", want: LoopOnFailureRestart},
		{name: "continue", input: "continue", want: LoopOnFailureContinue},
		{name: "whitespace padded", input: "  CONTINUE ", want: LoopOnFailureContinue},
		{name: "stop is not supported", input: "stop", wantErr: true},
		{name: "invalid value", input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLoopOnFailure(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestLoopOnFailureNames(t *testing.T) {
	t.Parallel()

	require.Equal(t, "restart", LoopOnFailureRestart.String())
	require.Equal(t, "continue", LoopOnFailureContinue.String())
}
