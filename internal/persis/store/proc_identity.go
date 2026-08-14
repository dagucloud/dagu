// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"github.com/dagucloud/dagu/v2/internal/proc"
)

const (
	procEntryIdentityCollection = "collection"
)

func collectionProcEntryID(recordID string) proc.ProcEntryID {
	return proc.NewStoreEntryID(procEntryIdentityCollection, recordID)
}

func procEntrySortKey(entry proc.ProcEntry) string {
	if !entry.Identity.IsZero() {
		return entry.Identity.String()
	}
	return entry.GroupName + "|" + entry.Meta.Root().String() + "|" + entry.Meta.Name + "|" + entry.Meta.DAGRunID + "|" + entry.Meta.AttemptID
}
