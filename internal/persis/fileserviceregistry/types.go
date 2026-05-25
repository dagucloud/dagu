// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileserviceregistry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// instanceInfo represents the information stored for each service instance
type instanceInfo struct {
	ID        string             `json:"id"`
	Host      string             `json:"host"`
	Port      int                `json:"port"`
	PID       int                `json:"pid"`
	Status    exec.ServiceStatus `json:"status"`
	StartedAt time.Time          `json:"startedAt"`
}

// instanceFilePath returns the file path for an instance
func instanceFilePath(baseDir, serviceName, instanceID string) string {
	return filepath.Join(baseDir, serviceName, fmt.Sprintf("%s.json", fileutil.SafeName(instanceID)))
}

// writeInstanceFile writes instance information to a file atomically
func writeInstanceFile(filename string, info *instanceInfo) error {
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	if err := fileutil.WriteJSONAtomic(filename, info, 0600); err != nil {
		return fmt.Errorf("failed to write instance file: %w", err)
	}
	return nil
}

// readInstanceFile reads instance information from a file
func readInstanceFile(path string) (*instanceInfo, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read instance file: %w", err)
	}

	var info instanceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal instance info: %w", err)
	}

	return &info, nil
}

// removeInstanceFile removes an instance file
func removeInstanceFile(filename string) error {
	if err := fileutil.Remove(filename); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove instance file: %w", err)
	}
	return nil
}
