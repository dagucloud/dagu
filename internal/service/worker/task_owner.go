// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

func taskOwner(task *coordinatorv1.Task) (serviceregistry.HostInfo, error) {
	if task == nil {
		return serviceregistry.HostInfo{}, nil
	}

	hasHost := task.OwnerCoordinatorHost != ""
	hasPort := task.OwnerCoordinatorPort != 0
	if task.OwnerCoordinatorId == "" && !hasHost && !hasPort {
		return serviceregistry.HostInfo{}, nil
	}
	if !hasHost || !hasPort {
		return serviceregistry.HostInfo{}, fmt.Errorf(
			"task has incomplete owner coordinator metadata: host=%t port=%t",
			hasHost,
			hasPort,
		)
	}

	return serviceregistry.HostInfo{
		ID:   task.OwnerCoordinatorId,
		Host: task.OwnerCoordinatorHost,
		Port: int(task.OwnerCoordinatorPort),
	}, nil
}
