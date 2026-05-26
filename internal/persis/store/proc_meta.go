// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	procRecordPrefix = "proc_"
	procDateTimeUTC  = "20060102_150405"
)

var procSafeAttemptIDPattern = regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)

func validateProcMeta(meta exec.ProcMeta) error {
	if meta.Name == "" {
		return fmt.Errorf("proc meta name is required")
	}
	if err := exec.ValidateDAGRunID(meta.DAGRunID); err != nil {
		return fmt.Errorf("invalid proc meta dag run id: %w", err)
	}
	if meta.AttemptID == "" {
		return fmt.Errorf("proc meta attempt id is required")
	}
	if !procSafeAttemptIDPattern.MatchString(meta.AttemptID) {
		return fmt.Errorf("proc meta attempt id must only contain alphanumeric characters, dashes, and underscores")
	}
	if meta.StartedAt <= 0 {
		return fmt.Errorf("proc meta started at must be > 0")
	}
	if (meta.RootName == "") != (meta.RootDAGRunID == "") {
		return fmt.Errorf("proc meta root name and root dag run id must both be set or both be empty")
	}
	if meta.RootDAGRunID != "" {
		if err := exec.ValidateDAGRunID(meta.RootDAGRunID); err != nil {
			return fmt.Errorf("invalid proc meta root dag run id: %w", err)
		}
	}
	return nil
}

func procRecordID(groupName string, meta exec.ProcMeta, t time.Time) string {
	return filepath.ToSlash(filepath.Join(groupName, meta.Name, procRecordName(meta, t)))
}

func procRecordName(meta exec.ProcMeta, t time.Time) string {
	return fmt.Sprintf("%s%sZ_%s_%s",
		procRecordPrefix,
		t.UTC().Format(procDateTimeUTC),
		hex.EncodeToString([]byte(meta.DAGRunID)),
		hex.EncodeToString([]byte(meta.AttemptID)),
	)
}
