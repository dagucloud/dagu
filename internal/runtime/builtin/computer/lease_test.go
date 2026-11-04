// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every application's lock lies in the directory of application locks,
// whatever the application is named, and applications that differ only in
// case share one.
func TestAppLockNames(t *testing.T) {
	t.Parallel()

	apps := filepath.Join(t.TempDir(), appLocksDir)
	for _, app := range []string{"EXCEL", "Microsoft Excel", "a/b", `c:\d`, ".", "..", "...", ""} {
		lock := filepath.Join(apps, appLockName(app))
		assert.Equal(t, apps, filepath.Dir(lock), "%q", app)
	}
	assert.Equal(t, appLockName("Excel"), appLockName("EXCEL"))
}
