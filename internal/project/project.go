// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package project defines projects, the rules for their names, and the
// registry that lists them.
package project

import "time"

// Project is a registered project.
type Project struct {
	// ID is a random UUID. It never changes and is never reused.
	ID string
	// Name is unique without regard to case and never changes.
	Name string
	// DisplayName is the name pages show.
	DisplayName string
	// CreatedAt is when the project was registered.
	CreatedAt time.Time
	// MigratedFrom is the name of the v2 workspace a migrated project came
	// from. It is empty for any other project.
	MigratedFrom string
	// MigratedDAGs names the DAGs that migration placed in the project,
	// by their v2 names. Their v2 webhook URLs reach the project.
	MigratedDAGs []string
	// SlotSeed goes into the run ID of each slot of the project's
	// schedules. It is paths.data_dir, spelled as configured, for the
	// default project and each migrated one, so their slots keep the run
	// IDs v2 gave them, and the ID for any other project. It is set when
	// the project is registered and never changes.
	SlotSeed string
	// Binding is the project's Dagu Cloud binding.
	Binding Binding
	// Deleting reports that a delete of the project is in progress.
	Deleting bool
}

// Binding links a project to a Dagu Cloud project. The zero value is
// unbound.
type Binding struct {
	// CloudProjectID identifies the Dagu Cloud project.
	CloudProjectID string
}
