// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

// DAGExists reports whether the run's DAG loader finds a DAG named name. It
// returns an error when it cannot tell, such as when the run has no loader.
func DAGExists(ctx context.Context, name string) (bool, error) {
	loader := GetDAGContext(ctx).DAGLoader
	if loader == nil {
		return false, errors.New("the run has no DAG loader")
	}
	_, err := loader.GetDAG(ctx, name)
	if errors.Is(err, persis.ErrDAGNotFound) {
		return false, nil
	}
	return err == nil, err
}
