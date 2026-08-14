// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

type recordVersionCollection interface {
	RecordVersion(ctx context.Context, id string) (string, error)
}

func collectionRecordVersion(ctx context.Context, col persis.Collection, id string) (string, bool, error) {
	versioned, ok := col.(recordVersionCollection)
	if !ok {
		return "", false, nil
	}
	version, err := versioned.RecordVersion(ctx, id)
	return version, true, err
}
