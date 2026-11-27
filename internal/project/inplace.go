// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import "context"

// InPlaceRecords reads the registry's records of the files that run in
// place: DAG files outside a project's DAG directory that the registry
// records as that project's. A record stays while its file is absent, and
// ends once the project's DAG directory holds a DAG file of the same name;
// neither method returns an ended record. Implementations are safe for
// concurrent use.
type InPlaceRecords interface {
	// InPlaceOwner returns the name, as stored, of the project that runs
	// the file at path in place, or "" when no record names the file. The
	// path is absolute, with its symbolic links resolved.
	InPlaceOwner(ctx context.Context, path string) (string, error)

	// InPlaceFiles returns the recorded paths of the files that run in
	// place as the project called name, given in any case, whether or not
	// each file exists.
	InPlaceFiles(ctx context.Context, name string) ([]string, error)
}
