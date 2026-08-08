// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOverlapPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    OverlapPolicy
		wantErr bool
	}{
		{name: "skip", input: "skip", want: OverlapPolicySkip},
		{name: "all", input: "all", want: OverlapPolicyAll},
		{name: "empty defaults to skip", input: "", want: OverlapPolicySkip},
		{name: "uppercase Skip", input: "Skip", want: OverlapPolicySkip},
		{name: "uppercase ALL", input: "ALL", want: OverlapPolicyAll},
		{name: "latest", input: "latest", want: OverlapPolicyLatest},
		{name: "uppercase LATEST", input: "LATEST", want: OverlapPolicyLatest},
		{name: "whitespace padded", input: "  skip  ", want: OverlapPolicySkip},
		{name: "invalid value", input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseOverlapPolicy(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
