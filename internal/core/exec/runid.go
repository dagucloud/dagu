// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"math/big"

	"github.com/google/uuid"
)

const (
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base62UUIDLen  = 22
)

// NewDAGRunID returns a compact UUIDv7-derived DAG-run ID.
func NewDAGRunID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return encodeBase62UUID(id), nil
}

func encodeBase62UUID(id uuid.UUID) string {
	var n big.Int
	n.SetBytes(id[:])

	base := big.NewInt(int64(len(base62Alphabet)))
	var rem big.Int
	out := make([]byte, base62UUIDLen)
	for i := len(out) - 1; i >= 0; i-- {
		n.DivMod(&n, base, &rem)
		out[i] = base62Alphabet[rem.Int64()]
	}
	return string(out)
}
