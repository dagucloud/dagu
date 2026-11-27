// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	stepFieldConnection = "connection"
	stepFieldServer     = "server"
)

// connectionStepTypes are the executors whose with block takes a
// connection.
var connectionStepTypes = []string{"mcp"}

// collectConnections records the connections that steps name in
// with.connection, including foreach bodies and handlers. Each connection is
// listed once, with the servers that steps name beside it. Names and servers
// must be literals, so that a run checks them before any value is evaluated
// and building a DAG reads no connection record.
func (s *dagBuildState) collectConnections() {
	var refs []ir.ConnectionRef
	walkSteps(s.result, func(step ir.Step, path string) {
		if !slices.Contains(connectionStepTypes, step.ExecutorConfig.Type) {
			return
		}
		cfg := step.ExecutorConfig.Config
		raw, ok := cfg[stepFieldConnection]
		if !ok {
			return
		}
		path += ".with."
		name, err := literalName(raw)
		if err != nil {
			s.errs = append(s.errs, ir.NewValidationError(path+stepFieldConnection, raw, err))
			return
		}
		i := slices.IndexFunc(refs, func(ref ir.ConnectionRef) bool { return ref.Name == name })
		if i < 0 {
			i = len(refs)
			refs = append(refs, ir.ConnectionRef{Name: name})
		}
		rawServer, ok := cfg[stepFieldServer]
		if !ok {
			return
		}
		server, err := literalName(rawServer)
		if err != nil {
			s.errs = append(s.errs, ir.NewValidationError(path+stepFieldServer, rawServer, err))
			return
		}
		if !slices.Contains(refs[i].Servers, server) {
			refs[i].Servers = append(refs[i].Servers, server)
		}
	})
	s.result.Connections = refs
}

// literalName returns raw as a connection or service name.
func literalName(raw any) (string, error) {
	name, ok := raw.(string)
	if !ok {
		return "", errors.New("must be a name")
	}
	if value.HasAnyReference(name) {
		return "", fmt.Errorf("%q must be a literal name, not a reference", name)
	}
	if connection.ValidateName(name) != nil {
		return "", fmt.Errorf("%q must be up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit", name)
	}
	return name, nil
}
