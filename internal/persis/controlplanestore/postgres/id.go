// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"fmt"

	"github.com/google/uuid"
)

func parseUUIDv7(value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, err
	}
	if id.Version() != 7 {
		return uuid.Nil, fmt.Errorf("uuid is not v7")
	}
	return id, nil
}

func newUUIDv7String() (string, uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", uuid.Nil, err
	}
	return id.String(), id, nil
}

func ensureUUIDv7String(value string) (string, uuid.UUID, error) {
	if value != "" {
		id, err := parseUUIDv7(value)
		if err != nil {
			return "", uuid.Nil, err
		}
		return value, id, nil
	}
	return newUUIDv7String()
}
