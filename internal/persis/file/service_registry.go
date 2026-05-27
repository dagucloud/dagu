// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/fileserviceregistry"
)

// NewServiceRegistry wires the file-backed service registry from application config.
func NewServiceRegistry(cfg *config.Config) exec.ServiceRegistry {
	return fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
}
