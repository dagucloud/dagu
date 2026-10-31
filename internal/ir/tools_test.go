// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateWorkerAbsPath(t *testing.T) {
	t.Parallel()
	for path, valid := range map[string]bool{
		"/etc/registry.yaml":           true,
		" /etc/registry.yaml ":         true,
		`C:\dagu\registry.yaml`:        true,
		`c:/dagu/registry.yaml`:        true,
		`\\server\share\registry.yaml`: true,
		`\\server\share`:               true,
		"":                             false,
		" ":                            false,
		"registry.yaml":                false,
		`C:registry.yaml`:              false,
		`\registry.yaml`:               false,
		`\\server`:                     false,
		`\\server\`:                    false,
		`\\server\\share`:              false,
		`\\\share`:                     false,
	} {
		t.Run(path, func(t *testing.T) {
			err := ValidateWorkerAbsPath(path)
			if valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
