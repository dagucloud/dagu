// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package container tells whether Dagu runs inside an application
// container.
package container

import "os"

// markerFiles are created inside containers by Docker and Podman.
var markerFiles = []string{"/.dockerenv", "/run/.containerenv"}

// Detected reports whether the process runs inside an application container.
// Stopping a container ends every process in it, and it is updated by
// replacing its image rather than its binary.
func Detected() bool {
	// Kubernetes sets this in every pod, whichever container runtime runs it.
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	for _, marker := range markerFiles {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
	}
	return false
}
