// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
)

const (
	webhookPayloadParam = "WEBHOOK_PAYLOAD"
	webhookPayloadFile  = "WEBHOOK_PAYLOAD_FILE"
)

// stepPayload is what a webhook run's steps see of the request body.
type stepPayload struct {
	// params are the params the steps see.
	params []string
	// paramsJSON, when set, replaces DAG_PARAMS_JSON, which then leaves the
	// body out too.
	paramsJSON *string
	// fileEnv is the WEBHOOK_PAYLOAD_FILE entry, empty for a run without a
	// webhook body.
	fileEnv string
	// cleanup removes the payload file.
	cleanup func()
}

// webhookPayload gives a webhook run's steps the body as a file, since one
// environment variable holds only so much. When the body would not fit in a
// variable, the steps' params and params JSON leave it out, since starting a
// step with it would fail. A run without a webhook body gets its params as
// they are.
func webhookPayload(params []string, paramsJSON string) (stepPayload, error) {
	i := slices.IndexFunc(params, func(p string) bool { return strings.HasPrefix(p, webhookPayloadParam+"=") })
	if i < 0 {
		return stepPayload{params: params, cleanup: func() {}}, nil
	}
	dir, err := os.MkdirTemp("", "dagu-webhook-")
	if err != nil {
		return stepPayload{}, fmt.Errorf("failed to create webhook payload directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "payload")
	payload := strings.TrimPrefix(params[i], webhookPayloadParam+"=")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		cleanup()
		return stepPayload{}, fmt.Errorf("failed to write webhook payload file: %w", err)
	}
	result := stepPayload{params: params, fileEnv: webhookPayloadFile + "=" + path, cleanup: cleanup}
	if len(params[i]) >= maxEnvEntry() {
		result.params = slices.Delete(slices.Clone(params), i, i+1)
		result.paramsJSON = withoutParam(paramsJSON, webhookPayloadParam)
	}
	return result, nil
}

// withoutParam returns params JSON without name, or nil when it is not an
// object holding name.
func withoutParam(paramsJSON, name string) *string {
	var params map[string]json.RawMessage
	if json.Unmarshal([]byte(paramsJSON), &params) != nil {
		return nil
	}
	if _, ok := params[name]; !ok {
		return nil
	}
	delete(params, name)
	data, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	trimmed := string(data)
	return &trimmed
}

// maxEnvEntry bounds one NAME=VALUE environment entry: Linux refuses to start
// a process with an entry of 128 KiB or more, and Windows caps a variable at
// 32,767 characters. macOS is held to the Linux bound, well inside its limit
// on the whole environment.
func maxEnvEntry() int {
	if goruntime.GOOS == "windows" {
		return 32_767
	}
	return 128 << 10
}
