// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

// The event data of a service DAG says so, also when the DAG has no file name.
func TestDAGRunEventDataServiceDAG(t *testing.T) {
	t.Parallel()

	service := dagRunEventData(&ir.DAG{Name: "svc", RestartPolicy: ir.RestartPolicyAlways, SourceFile: "/dags/svc.yaml"})
	assert.Equal(t, true, service[eventstore.ServiceDAGDataKey])
	assert.Equal(t, "svc", service[eventstore.DAGFileNameDataKey])

	noFile := dagRunEventData(&ir.DAG{Name: "svc", RestartPolicy: ir.RestartPolicyUnlessStopped})
	assert.Equal(t, true, noFile[eventstore.ServiceDAGDataKey])

	normal := dagRunEventData(&ir.DAG{Name: "plain", SourceFile: "/dags/plain.yaml"})
	assert.NotContains(t, normal, eventstore.ServiceDAGDataKey)
	assert.Nil(t, dagRunEventData(&ir.DAG{Name: "plain"}))
}
