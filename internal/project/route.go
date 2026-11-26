// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"errors"
	"fmt"
	"strings"
)

// Errors for the runs a project other than the default one cannot make yet,
// since the coordinator and the scheduler serve only the default project.
var (
	ErrWorkerRun = errors.New("cannot run DAGs on workers yet (P12)")
	ErrQueuedRun = errors.New("cannot queue runs yet (P10)")
)

// RefuseOtherProject returns an error wrapping err for the project called
// name, unless name is empty, as while projects are off, or names the
// default project.
func RefuseOtherProject(name string, err error) error {
	if name == "" || strings.EqualFold(name, DefaultName) {
		return nil
	}
	return fmt.Errorf("project %s %w", name, err)
}
