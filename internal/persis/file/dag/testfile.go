// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/yamlutil"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/goccy/go-yaml"
)

// WarnTestNamedDAGs logs one warning for each file beneath root, found under
// the rules of opts, that discovery skips as a test file but that holds a
// DAG: top-level steps and no cases. A file that is not valid YAML is not
// reported.
func WarnTestNamedDAGs(ctx context.Context, root string, opts DiscoveryOptions) {
	scan, err := discover(root, opts, spec.IsTestFile)
	if err != nil {
		return
	}
	for _, file := range scan.Files {
		data, err := fileutil.ReadFile(file.ResolvedPath)
		if err != nil || !readsAsDAG(data) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(file.RelPath))
		logger.Warn(ctx, path+" "+spec.TestNamedDAGHint(path))
	}
}

// readsAsDAG reports whether the first document of data, as the DAG loader
// reads it, has top-level steps and no cases.
func readsAsDAG(data []byte) bool {
	var top map[string]yaml.RawMessage
	data = yamlutil.ClearEmptyDocumentSeparators(yamlutil.TrimByteOrderMark(data))
	if err := yaml.Unmarshal(data, &top); err != nil {
		return false
	}
	_, steps := top["steps"]
	_, cases := top["cases"]
	return steps && !cases
}
