// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/ir"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
)

const (
	stepFieldConnection = "connection"
	stepFieldServer     = "server"
)

// connectionStepTypes are the executors whose with block takes a
// connection.
var connectionStepTypes = []string{"mcp"}

// collectConnections records the connections that steps name in
// with.connection, including foreach bodies and handlers, and that secrets
// of the connection provider name in key. Each connection is listed once,
// with the servers that steps name beside it. Names and servers must be
// literals, so that a run checks them before any value is evaluated and
// building a DAG reads no connection record. A value that is not a string
// is left to the executor's config schema, which rejects it.
func (s *dagBuildState) collectConnections() {
	var refs []ir.ConnectionRef
	indexOf := func(name string) int {
		i := slices.IndexFunc(refs, func(ref ir.ConnectionRef) bool { return ref.Name == name })
		if i < 0 {
			i = len(refs)
			refs = append(refs, ir.ConnectionRef{Name: name})
		}
		return i
	}
	walkSteps(s.result, func(step ir.Step, path string) {
		if !slices.Contains(connectionStepTypes, step.ExecutorConfig.Type) {
			return
		}
		cfg := step.ExecutorConfig.Config
		name, ok := cfg[stepFieldConnection].(string)
		if !ok {
			return
		}
		path += ".with."
		if err := validateLiteralName(name); err != nil {
			s.errs = append(s.errs, ir.NewValidationError(path+stepFieldConnection, name, err))
			return
		}
		i := indexOf(name)
		server, ok := cfg[stepFieldServer].(string)
		if !ok {
			return
		}
		if err := validateLiteralName(server); err != nil {
			s.errs = append(s.errs, ir.NewValidationError(path+stepFieldServer, server, err))
			return
		}
		if !slices.Contains(refs[i].Servers, server) {
			refs[i].Servers = append(refs[i].Servers, server)
		}
	})
	for i, sec := range s.result.Secrets {
		if sec.Provider != secretref.ProviderConnection {
			continue
		}
		if err := validateLiteralName(sec.Key); err != nil {
			s.errs = append(s.errs, ir.NewValidationError(fmt.Sprintf("secrets[%d].key", i), sec.Key, err))
			continue
		}
		indexOf(sec.Key)
	}
	s.result.Connections = refs
}

// validateLiteralName reports whether name is a literal connection or
// service name.
func validateLiteralName(name string) error {
	if value.HasAnyReference(name) {
		return fmt.Errorf("%q must be a literal name, not a reference", name)
	}
	if connection.ValidateName(name) != nil {
		return fmt.Errorf("%q must be up to 63 lowercase letters, digits, and hyphens, starting with a letter or digit", name)
	}
	return nil
}
