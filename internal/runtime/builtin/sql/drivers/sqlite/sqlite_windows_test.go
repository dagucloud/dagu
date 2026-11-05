// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The lock path of a Windows file: URI is the drive or UNC path SQLite opens.
func TestExtractDBPathWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dsn  string
		want string
	}{
		{"file:/C:/Users/dagu/work/app.db?mode=rwc", `C:\Users\dagu\work\app.db`},
		{"file:////server/share/work/app.db", `\\server\share\work\app.db`},
	}
	for _, tt := range tests {
		t.Run(tt.dsn, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractDBPath(tt.dsn))
		})
	}
}
