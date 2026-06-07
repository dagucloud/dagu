package tests_test

import (
	"testing"

	dagutest "github.com/dagucloud/dagu/tests/internal"
)

func Test002Schema(t *testing.T) {
	t.Parallel()

	t.Run("entrypoint name is forbidden", func(t *testing.T) {
		t.Parallel()

		dagu := dagutest.New(t, "002_schema")

		result := dagu.Run("workflow", "validate", ".dagu/entrypoint_name_forbidden.yaml")
		result.ExpectExitCode(1)
		result.ExpectStdout("")
		result.ExpectStderrContains("entrypoint", "name")
		dagu.ExpectNoFile("executed.txt")
	})
}
