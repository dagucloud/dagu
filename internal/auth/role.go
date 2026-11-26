// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import "fmt"

// Role represents a user's role in the system.
// Roles determine what actions a user can perform.
//
// Role hierarchy (most to least privileged):
//   - admin: Full system access including user management
//   - manager: Can create, edit, delete, run, and stop DAGs, and view audit logs
//   - developer: Can create, edit, delete, run, and stop DAGs
//   - operator: Can run and stop DAGs (execute only)
//   - viewer: Read-only access to DAGs and status
type Role string

const (
	// RoleAdmin has full access to all resources including user management.
	RoleAdmin Role = "admin"
	// RoleManager can create, edit, delete, run, and stop DAGs, and view audit logs.
	RoleManager Role = "manager"
	// RoleDeveloper can create, edit, delete, run, and stop DAGs.
	RoleDeveloper Role = "developer"
	// RoleOperator can run and stop DAGs (execute only, no edit).
	RoleOperator Role = "operator"
	// RoleViewer can only view DAGs and execution history (read-only).
	RoleViewer Role = "viewer"
	// RoleNone represents an unset or unauthenticated role.
	RoleNone Role = ""
)

// allRoles contains all valid roles for iteration and validation.
var allRoles = []Role{RoleAdmin, RoleManager, RoleDeveloper, RoleOperator, RoleViewer}

// AllRoles returns a copy of all valid roles.
func AllRoles() []Role {
	roles := make([]Role, len(allRoles))
	copy(roles, allRoles)
	return roles
}

// Valid returns true if the role is a known valid role.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleManager, RoleDeveloper, RoleOperator, RoleViewer:
		return true
	case RoleNone:
		return false
	}
	return false
}

// String returns the string representation of the role.
func (r Role) String() string {
	return string(r)
}

// IsSet returns true if the role has been assigned (is not empty).
func (r Role) IsSet() bool {
	return r != RoleNone
}

// CanWrite returns true if the role can create, edit, or delete DAGs.
func (r Role) CanWrite() bool {
	return r == RoleAdmin || r == RoleManager || r == RoleDeveloper
}

// CanExecute returns true if the role can run or stop DAGs.
func (r Role) CanExecute() bool {
	return r == RoleAdmin || r == RoleManager || r == RoleDeveloper || r == RoleOperator
}

// CanManageAudit returns true if the role can view audit logs.
func (r Role) CanManageAudit() bool {
	return r == RoleAdmin || r == RoleManager
}

// IsAdmin returns true if the role has administrative privileges (user management).
func (r Role) IsAdmin() bool {
	return r == RoleAdmin
}

// Outranks reports whether r is more privileged than other. Every valid role
// outranks RoleNone and roles this version does not know, which outrank no
// role.
func (r Role) Outranks(other Role) bool {
	return r.rank() > other.rank()
}

// rank orders roles by privilege. RoleNone and unknown roles rank lowest.
func (r Role) rank() int {
	switch r {
	case RoleAdmin:
		return 5
	case RoleManager:
		return 4
	case RoleDeveloper:
		return 3
	case RoleOperator:
		return 2
	case RoleViewer:
		return 1
	case RoleNone:
		return 0
	}
	return 0
}

// ParseRole converts a string to a Role.
// If the input is not "admin", "manager", "developer", "operator", or "viewer", it returns an error describing the valid options.
func ParseRole(s string) (Role, error) {
	role := Role(s)
	if !role.Valid() {
		return "", fmt.Errorf("invalid role: %q, must be one of: admin, manager, developer, operator, viewer", s)
	}
	return role, nil
}
