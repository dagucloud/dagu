// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCPULimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want int64
	}{
		{name: "Millicores", in: "500m", want: 500},
		{name: "WholeCores", in: "2", want: 2000},
		{name: "FractionalCores", in: "0.5", want: 500},
		{name: "MilliPrecisionCores", in: "0.001", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseCPULimit(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResourceLimitsUnmarshalJSONRecomputesLimits(t *testing.T) {
	t.Parallel()

	var resources Resources
	err := json.Unmarshal([]byte(`{"limits":{"cpu":" 500m ","memory":" 1Gi "}}`), &resources)
	require.NoError(t, err)
	require.NotNil(t, resources.Limits)
	assert.Equal(t, "500m", resources.Limits.CPU)
	assert.Equal(t, int64(500), resources.Limits.CPUMillis)
	assert.Equal(t, "1Gi", resources.Limits.Memory)
	assert.Equal(t, int64(1024*1024*1024), resources.Limits.MemoryBytes)
	assert.True(t, resources.HasLimits())
}

func TestResourceLimitsUnmarshalJSONRejectsInvalidLimits(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"cpu":"0.5m"}`,
		`{"memory":"0"}`,
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			var limits ResourceLimits
			err := json.Unmarshal([]byte(tt), &limits)
			require.Error(t, err)
		})
	}
}

func TestParseCPULimitRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"0",
		"0m",
		"0.5m",
		"0.0005",
		"1.0001",
		"nope",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			_, err := ParseCPULimit(tt)
			require.Error(t, err)
		})
	}
}

func TestParseMemoryLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want int64
	}{
		{name: "Bytes", in: "512", want: 512},
		{name: "BinaryUnit", in: "1Gi", want: 1024 * 1024 * 1024},
		{name: "DecimalUnit", in: "1G", want: 1_000_000_000},
		{name: "FractionalBinaryUnit", in: "1.5Mi", want: 1572864},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseMemoryLimit(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
