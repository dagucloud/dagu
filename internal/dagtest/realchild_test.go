// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A real dag.run step and a real handler check the child they start; a
// child named by a reference, and one that does not resolve, make the case
// invalid. dag.enqueue starts no child.
func TestCheckRealChildren(t *testing.T) {
	t.Parallel()

	child, err := spec.LoadYAML(context.Background(), []byte(`
secrets:
  - name: TOKEN
    provider: env
    key: DAGU_DAGTEST_UNSET_TOKEN
steps:
  - id: write
    run: echo child
  - id: say
    action: log.write
    with:
      message: child
`), spec.DeferAllParamEvals())
	require.NoError(t, err)
	resolve := func(name string) (*ir.DAG, error) {
		if name == "child" {
			return child, nil
		}
		return nil, errors.New("not found")
	}
	file := loadTestFile(t, "children.test.yaml")
	c := file.Cases[0]
	require.Empty(t, c.Errors)

	childFix := "a child of a real step takes no mock; mock "
	assert.Equal(t, []string{
		"step call, child child, secret TOKEN: a real step may read it; " + childFix + "call instead",
		"step call, child child, step write: command has side effects; " + childFix + "call instead",
		"step dynamic: child DAG ${params.child} is known only during the run; mock it",
		"step missing: child DAG absent cannot be resolved: not found; mock it",
		"handler_on.exit, child child, secret TOKEN: a real step may read it; " + childFix + "handler_on.exit instead",
		"handler_on.exit, child child, step write: command has side effects; " + childFix + "handler_on.exit instead",
	}, findingStrings(dagtest.CheckRealChildren(file.DAG, c, resolve)))
}
