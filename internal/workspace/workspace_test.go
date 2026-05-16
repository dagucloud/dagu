// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspace

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBaseConfigPathValidatesWorkspaceName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	assert.Equal(t,
		filepath.Join(root, BaseConfigDirName, "ops", BaseConfigFileName),
		BaseConfigPath(root, "ops"),
	)

	assert.Empty(t, BaseConfigPath(root, "../ops"))
	assert.Empty(t, BaseConfigPath(root, "default"))
	assert.Empty(t, BaseConfigPath(root, "all"))
	assert.Empty(t, BaseConfigPath(root, ""))
}

func TestValidateNameRejectsReservedGlobalName(t *testing.T) {
	t.Parallel()

	assert.Error(t, ValidateName("global"))
	assert.Error(t, ValidateName("GLOBAL"))
}
