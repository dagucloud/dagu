// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"testing"
)

func TestRole_Valid(t *testing.T) {
	tests := []struct {
		role  Role
		valid bool
	}{
		{RoleAdmin, true},
		{RoleManager, true},
		{RoleDeveloper, true},
		{RoleOperator, true},
		{RoleViewer, true},
		{Role("invalid"), false},
		{Role(""), false},
		{Role("ADMIN"), false},  // case sensitive
		{Role("editor"), false}, // old role name
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.Valid(); got != tt.valid {
				t.Errorf("Role(%q).Valid() = %v, want %v", tt.role, got, tt.valid)
			}
		})
	}
}

func TestRole_CanWrite(t *testing.T) {
	tests := []struct {
		role     Role
		canWrite bool
	}{
		{RoleAdmin, true},
		{RoleManager, true},
		{RoleDeveloper, true},
		{RoleOperator, false},
		{RoleViewer, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.CanWrite(); got != tt.canWrite {
				t.Errorf("Role(%q).CanWrite() = %v, want %v", tt.role, got, tt.canWrite)
			}
		})
	}
}

func TestRole_CanExecute(t *testing.T) {
	tests := []struct {
		role       Role
		canExecute bool
	}{
		{RoleAdmin, true},
		{RoleManager, true},
		{RoleDeveloper, true},
		{RoleOperator, true},
		{RoleViewer, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.CanExecute(); got != tt.canExecute {
				t.Errorf("Role(%q).CanExecute() = %v, want %v", tt.role, got, tt.canExecute)
			}
		})
	}
}

func TestRole_IsAdmin(t *testing.T) {
	tests := []struct {
		role    Role
		isAdmin bool
	}{
		{RoleAdmin, true},
		{RoleManager, false},
		{RoleDeveloper, false},
		{RoleOperator, false},
		{RoleViewer, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.IsAdmin(); got != tt.isAdmin {
				t.Errorf("Role(%q).IsAdmin() = %v, want %v", tt.role, got, tt.isAdmin)
			}
		})
	}
}

func TestRole_Outranks(t *testing.T) {
	ordered := []Role{RoleAdmin, RoleManager, RoleDeveloper, RoleOperator, RoleViewer}
	for i, higher := range ordered {
		for _, lower := range ordered[i+1:] {
			if !higher.Outranks(lower) || lower.Outranks(higher) {
				t.Errorf("%q must outrank %q", higher, lower)
			}
		}
		if higher.Outranks(higher) {
			t.Errorf("%q must not outrank itself", higher)
		}
		for _, unranked := range []Role{RoleNone, Role("custom")} {
			if !higher.Outranks(unranked) || unranked.Outranks(higher) {
				t.Errorf("%q must outrank %q", higher, unranked)
			}
		}
	}
	if Role("custom").Outranks(RoleNone) || RoleNone.Outranks(Role("custom")) {
		t.Error("RoleNone and an unknown role must rank equal")
	}
}

func TestRole_CanManageAudit(t *testing.T) {
	tests := []struct {
		role           Role
		canManageAudit bool
	}{
		{RoleAdmin, true},
		{RoleManager, true},
		{RoleDeveloper, false},
		{RoleOperator, false},
		{RoleViewer, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			if got := tt.role.CanManageAudit(); got != tt.canManageAudit {
				t.Errorf("Role(%q).CanManageAudit() = %v, want %v", tt.role, got, tt.canManageAudit)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	tests := []struct {
		input   string
		want    Role
		wantErr bool
	}{
		{"admin", RoleAdmin, false},
		{"manager", RoleManager, false},
		{"developer", RoleDeveloper, false},
		{"operator", RoleOperator, false},
		{"viewer", RoleViewer, false},
		{"invalid", "", true},
		{"", "", true},
		{"ADMIN", "", true},
		{"editor", "", true}, // old role name
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRole(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRole(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseRole(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAllRoles(t *testing.T) {
	roles := AllRoles()
	if len(roles) != 5 {
		t.Errorf("AllRoles() returned %d roles, want 5", len(roles))
	}

	// Ensure modifying returned slice doesn't affect internal state
	roles[0] = "modified"
	originalRoles := AllRoles()
	if originalRoles[0] == "modified" {
		t.Error("AllRoles() returned a reference to internal state")
	}
}

func TestRole_String(t *testing.T) {
	tests := []struct {
		role Role
		want string
	}{
		{RoleAdmin, "admin"},
		{RoleManager, "manager"},
		{RoleDeveloper, "developer"},
		{RoleOperator, "operator"},
		{RoleViewer, "viewer"},
		{Role("custom"), "custom"},
		{Role(""), ""},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.role.String(); got != tt.want {
				t.Errorf("Role(%q).String() = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}
