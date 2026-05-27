// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"encoding/base64"
	"strings"

	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	procEntryIdentityCollection = "collection"
)

func collectionProcEntryID(recordID string) exec.ProcEntryID {
	return procEntryID(procEntryIdentityCollection, recordID)
}

func procEntryID(kind, value string) exec.ProcEntryID {
	if kind == "" || value == "" {
		return exec.ProcEntryID{}
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	return exec.NewProcEntryID(kind + ":" + encoded)
}

func procEntryIdentityValue(entry exec.ProcEntry, expectedKind string) (string, bool) {
	kind, value, ok := splitProcEntryID(entry.Identity)
	if !ok || kind != expectedKind {
		return "", false
	}
	return value, true
}

func procEntryIdentityKind(entry exec.ProcEntry) string {
	kind, _, ok := splitProcEntryID(entry.Identity)
	if !ok {
		return ""
	}
	return kind
}

func splitProcEntryID(id exec.ProcEntryID) (kind, value string, ok bool) {
	if id.IsZero() {
		return "", "", false
	}
	raw := id.String()
	kind, encoded, found := strings.Cut(raw, ":")
	if !found || kind == "" || encoded == "" {
		return "", "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return "", "", false
	}
	return kind, string(decoded), true
}

func procEntrySortKey(entry exec.ProcEntry) string {
	if !entry.Identity.IsZero() {
		return entry.Identity.String()
	}
	return entry.GroupName + "|" + entry.Meta.Root().String() + "|" + entry.Meta.Name + "|" + entry.Meta.DAGRunID + "|" + entry.Meta.AttemptID
}
