// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestSameCadence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0 2 * * *", "0 1 * * *", true},
		{"30 2 * * 1-5", "0 6 * * 1-5", true},
		{"CRON_TZ=Asia/Tokyo 0 2 * * *", "0 17 * * *", true},
		{"5 * * * *", "10 * * * *", true},
		{"0 2 * * *", "0 2 * * *", true},
		{"0 2 * * *", "0 2 * * 1-5", false},
		{"0 2 * * *", "0 2,14 * * *", false},
		{"0 2 * * *", "0 * * * *", false},
		{"*/30 * * * *", "*/15 * * * *", false},
		{"0 2 1 * *", "0 2 2 * *", false},
		{"@every 1h", "0 * * * *", false},
		{"0 2 * * MON-FRI", "0 6 * * 1,2,3,4,5", true},
		{"0 2 * * *", "@daily", true},
		{"0 2 1-31 * *", "0 6 * * *", false},
	} {
		assert.Equal(t, tc.want, ir.SameCadence(tc.a, tc.b), "%s vs %s", tc.a, tc.b)
	}
}
