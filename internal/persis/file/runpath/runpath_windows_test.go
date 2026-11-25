// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runpath_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
	"github.com/stretchr/testify/assert"
)

func TestWindowsPaths(t *testing.T) {
	project := runpath.Dirs{Log: `C:\Dagu\data\projects\default\logs`}
	m := runpath.New(project, runpath.Dirs{Log: `C:\Dagu\logs`}, true)

	// Windows ignores case in names and the volume letter.
	assert.Equal(t, "./etl/run/x.log", m.StoreLog(`c:\dagu\DATA\projects\default\logs\etl\run\x.log`))
	assert.Equal(t, `C:\Dagu\data\projects\default\logs\etl\run\x.log`, m.ResolveLog("./etl/run/x.log"))
	assert.Equal(t, `C:\Dagu\data\projects\default\logs\etl\run\x.log`, m.ResolveLog(`c:\DAGU\Logs\etl\run\x.log`))

	// A path on another volume or share is kept as written.
	for _, path := range []string{`D:\Dagu\logs\etl\x.log`, `\\host\share\logs\etl\x.log`} {
		assert.Equal(t, path, m.StoreLog(path))
		assert.Equal(t, path, m.ResolveLog(path))
	}
}
