// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package project

import (
	"errors"
	"fmt"
	"strings"
)

// ErrQueuedRun refuses the queued runs of a project other than the default
// one: the scheduler reads only the default project's queues.
var ErrQueuedRun = errors.New("cannot queue runs; only the default project's queues are processed")

// RefuseOtherProject returns an error wrapping err for the project called
// name, unless name is empty, as while projects are off, or names the
// default project.
func RefuseOtherProject(name string, err error) error {
	if name == "" || strings.EqualFold(name, DefaultName) {
		return nil
	}
	return fmt.Errorf("project %s %w", name, err)
}
