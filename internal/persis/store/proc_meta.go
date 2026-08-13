// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/proc"
)

const (
	procRecordPrefix = "proc_"
	procDateTimeUTC  = "20060102_150405"
)

func procRecordID(groupName string, meta proc.ProcMeta, t time.Time) string {
	return filepath.ToSlash(filepath.Join(groupName, meta.Name, procRecordName(meta, t)))
}

func procRecordName(meta proc.ProcMeta, t time.Time) string {
	return fmt.Sprintf("%s%sZ_%s_%s",
		procRecordPrefix,
		t.UTC().Format(procDateTimeUTC),
		hex.EncodeToString([]byte(meta.DAGRunID)),
		hex.EncodeToString([]byte(meta.AttemptID)),
	)
}
