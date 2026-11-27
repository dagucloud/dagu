// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"errors"
	"fmt"
	"strings"
)

// ErrWorkerRun is the error for a run of a project other than the default
// one sent to a worker: the coordinator records dispatched runs in the
// default project.
var ErrWorkerRun = errors.New("cannot run DAGs on workers; only the default project's runs are dispatched")

// RefuseOtherProject returns an error wrapping err for the project called
// name, unless name is empty, as while projects are off, or names the
// default project.
func RefuseOtherProject(name string, err error) error {
	if name == "" || strings.EqualFold(name, DefaultName) {
		return nil
	}
	return fmt.Errorf("project %s %w", name, err)
}
