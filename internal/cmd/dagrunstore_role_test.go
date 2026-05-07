// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/internal/persis/controlplanestore"
)

func TestControlPlaneStoreRoleForCommand(t *testing.T) {
	tests := []struct {
		name string
		want controlplanestore.Role
	}{
		{name: "server", want: controlplanestore.RoleServer},
		{name: "start-all", want: controlplanestore.RoleServer},
		{name: "scheduler", want: controlplanestore.RoleScheduler},
		{name: "start", want: controlplanestore.RoleAgent},
		{name: "restart", want: controlplanestore.RoleAgent},
		{name: "retry", want: controlplanestore.RoleAgent},
		{name: "dry", want: controlplanestore.RoleAgent},
		{name: "exec", want: controlplanestore.RoleAgent},
		{name: "worker", want: controlplanestore.RoleAgent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, controlPlaneStoreRoleForCommand(&cobra.Command{Use: tt.name}))
		})
	}
}
