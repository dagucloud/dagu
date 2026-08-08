// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	fileserviceregistry "github.com/dagucloud/dagu/v2/internal/persis/file/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// NewServiceRegistry wires the file-backed service registry from application config.
func NewServiceRegistry(cfg *config.Config) serviceregistry.ServiceRegistry {
	return fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
}
