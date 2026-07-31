// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

func taskOwner(task *coordinatorv1.Task) (exec.HostInfo, error) {
	if task == nil {
		return exec.HostInfo{}, nil
	}

	hasID := task.OwnerCoordinatorId != ""
	hasHost := task.OwnerCoordinatorHost != ""
	hasPort := task.OwnerCoordinatorPort != 0
	if !hasID && !hasHost && !hasPort {
		return exec.HostInfo{}, nil
	}
	if !hasID || !hasHost || !hasPort {
		return exec.HostInfo{}, fmt.Errorf(
			"task has incomplete owner coordinator metadata: id=%t host=%t port=%t",
			hasID,
			hasHost,
			hasPort,
		)
	}

	return exec.HostInfo{
		ID:   task.OwnerCoordinatorId,
		Host: task.OwnerCoordinatorHost,
		Port: int(task.OwnerCoordinatorPort),
	}, nil
}
