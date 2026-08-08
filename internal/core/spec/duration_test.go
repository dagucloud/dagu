// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "days expanded", input: "2d12h", want: 60 * time.Hour},
		{name: "all units", input: "1d2h30m", want: 26*time.Hour + 30*time.Minute},
		{name: "days only", input: "7d", want: 7 * 24 * time.Hour},
		{name: "zero rejected", input: "0h", wantErr: true},
		{name: "negative rejected", input: "-1h", wantErr: true},
		{name: "whitespace trimmed", input: "  2h  ", want: 2 * time.Hour},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "overflow day value", input: "99999999999999d", wantErr: true},
		{name: "overflow day with other units", input: "99999999999999d1h", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
