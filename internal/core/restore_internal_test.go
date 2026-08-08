// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restoredFields names the JSON-excluded fields a rebuild carries over, and
// buildOutcomeFields names the rest. Excluding a field from JSON is a decision
// about rebuilds, not a detail: configuration kept off disk has to come back,
// while anything describing the build that produced it has to stay behind.
// Neither choice is evident from the field, and getting it wrong is silent, so
// the guard below refuses any field that is in neither list.
var restoredFields = map[string]bool{
	"Env":                true,
	"Params":             true,
	"ParamsJSON":         true,
	"SMTP":               true,
	"SSH":                true,
	"S3":                 true,
	"Redis":              true,
	"RegistryAuths":      true,
	"Harness":            true,
	"Harnesses":          true,
	"Kubernetes":         true,
	"WorkingDirExplicit": true,
}

// The classification guard above proves every field is assigned a side, but not
// that either side is honoured. This covers that: one configuration field must
// cross, and the build outcome must not.
func TestRestoreUnpersistedFromLeavesBuildOutcomeBehind(t *testing.T) {
	t.Parallel()

	// Stands in for a DAG decoded from dag.json, whose build outcome is its own.
	dst := &DAG{
		EnvEvaluated:  true,
		BuildErrors:   []error{errors.New("outcome of restoring")},
		BuildWarnings: []string{"warning from restoring"},
	}

	// Stands in for the same DAG rebuilt from its source.
	src := &DAG{
		SSH:           &SSHConfig{Host: "ssh.example.com"},
		EnvEvaluated:  false,
		BuildErrors:   []error{errors.New("outcome of rebuilding")},
		BuildWarnings: []string{"warning from rebuilding"},
	}

	dst.RestoreUnpersistedFrom(src)

	// Configuration kept off disk exists only on the rebuilt DAG.
	require.NotNil(t, dst.SSH)
	assert.Equal(t, "ssh.example.com", dst.SSH.Host)

	// The rebuild's outcome describes the rebuild, not the run being restored.
	assert.True(t, dst.EnvEvaluated)
	require.Len(t, dst.BuildErrors, 1)
	assert.EqualError(t, dst.BuildErrors[0], "outcome of restoring")
	assert.Equal(t, []string{"warning from restoring"}, dst.BuildWarnings)
}

func TestEveryJSONExcludedFieldIsClassified(t *testing.T) {
	t.Parallel()

	omitted := make(map[string]bool)
	for field := range reflect.TypeFor[DAG]().Fields() {
		if !field.IsExported() || field.Tag.Get("json") != "-" {
			continue
		}
		omitted[field.Name] = true

		switch {
		case restoredFields[field.Name] && buildOutcomeFields[field.Name]:
			t.Errorf("%s is named in both restoredFields and buildOutcomeFields", field.Name)
		case !restoredFields[field.Name] && !buildOutcomeFields[field.Name]:
			t.Errorf("%s is excluded from JSON, so only a rebuild can supply it. Name it in "+
				"restoredFields, or in buildOutcomeFields if it describes the build rather "+
				"than the configuration.", field.Name)
		}
	}

	for name := range restoredFields {
		if !omitted[name] {
			t.Errorf("%s is no longer excluded from JSON; drop it from restoredFields", name)
		}
	}
	for name := range buildOutcomeFields {
		if !omitted[name] {
			t.Errorf("%s is no longer excluded from JSON; drop it from buildOutcomeFields", name)
		}
	}
}
