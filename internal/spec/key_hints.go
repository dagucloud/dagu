// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"strings"
)

var legacyToSnakeCaseKey = map[string]string{
	"workingDir":            "working_dir",
	"skipIfSuccessful":      "skip_if_successful",
	"catchupWindow":         "catchup_window",
	"overlapPolicy":         "overlap_policy",
	"logDir":                "log_dir",
	"artifactDir":           "artifacts.dir",
	"enableArtifact":        "artifacts.enabled",
	"logOutput":             "log_output",
	"handlerOn":             "handler_on",
	"mailOn":                "mail_on",
	"errorMail":             "error_mail",
	"infoMail":              "info_mail",
	"waitMail":              "wait_mail",
	"timeoutSec":            "timeout_sec",
	"delaySec":              "delay_sec",
	"restartWaitSec":        "restart_wait_sec",
	"histRetentionDays":     "hist_retention_days",
	"histRetentionRuns":     "hist_retention_runs",
	"maxActiveRuns":         "max_active_runs",
	"maxActiveSteps":        "max_active_steps",
	"maxCleanUpTimeSec":     "max_clean_up_time_sec",
	"maxOutputSize":         "max_output_size",
	"runConfig":             "run_config",
	"forwardHeaders":        "webhook.forward_headers",
	"workerSelector":        "worker_selector",
	"registryAuths":         "registry_auths",
	"shellPackages":         "shell_packages",
	"continueOn":            "continue_on",
	"retryPolicy":           "retry_policy",
	"repeatPolicy":          "repeat_policy",
	"mailOnError":           "mail_on_error",
	"signalOnStop":          "signal_on_stop",
	"intervalSec":           "interval_sec",
	"exitCode":              "exit_code",
	"maxIntervalSec":        "max_interval_sec",
	"markSuccess":           "mark_success",
	"maxConcurrent":         "max_concurrent",
	"disableParamEdit":      "disable_param_edit",
	"disableRunIdEdit":      "disable_run_id_edit",
	"attachLogs":            "attach_logs",
	"pullPolicy":            "pull_policy",
	"keepContainer":         "keep_container",
	"waitFor":               "wait_for",
	"logPattern":            "log_pattern",
	"restartPolicy":         "restart_policy",
	"startPeriod":           "start_period",
	"strictHostKey":         "strict_host_key",
	"knownHostFile":         "known_host_file",
	"accessKeyId":           "access_key_id",
	"secretAccessKey":       "secret_access_key",
	"sessionToken":          "session_token",
	"forcePathStyle":        "force_path_style",
	"disableSSL":            "disable_ssl",
	"tlsSkipVerify":         "tls_skip_verify",
	"sentinelMaster":        "sentinel_master",
	"sentinelAddrs":         "sentinel_addrs",
	"clusterAddrs":          "cluster_addrs",
	"maxRetries":            "max_retries",
	"budgetTokens":          "budget_tokens",
	"includeInOutput":       "include_in_output",
	"maxTokens":             "max_tokens",
	"topP":                  "top_p",
	"baseURL":               "base_url",
	"apiKeyName":            "api_key_name",
	"maxToolIterations":     "max_tool_iterations",
	"maxContextTokens":      "max_context_tokens",
	"observationMaxBytes":   "observation_max_bytes",
	"observationKeepRecent": "observation_keep_recent",
}

// removedKeyHint maps keys that were renamed or removed (not just re-cased) to
// a complete replacement hint.
var removedKeyHint = map[string]string{
	"precondition": `"precondition" is no longer supported; use "preconditions"`,
	"dir":          `"dir" is no longer supported; use "working_dir"`,
	"executor":     `"executor" has been removed; use "action" and pass executor options under "with"`,
}

// withLegacyKeyHint appends replacement hints to decoder errors that report
// unknown keys, covering both camelCase-to-snake_case renames and removed keys.
func withLegacyKeyHint(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()
	const marker = "has invalid keys:"
	_, after, ok := strings.Cut(msg, marker)
	if !ok {
		return err
	}

	raw := strings.TrimSpace(after)
	if raw == "" {
		return err
	}

	keys := strings.Split(raw, ",")
	snakeCasePairs := make([]string, 0, len(keys))
	clauses := make([]string, 0, len(keys))
	for _, key := range keys {
		k := strings.TrimSpace(strings.Trim(key, `"'`))
		if k == "" {
			continue
		}
		if hint, ok := removedKeyHint[k]; ok {
			clauses = append(clauses, hint)
			continue
		}
		if snake, ok := legacyToSnakeCaseKey[k]; ok {
			snakeCasePairs = append(snakeCasePairs, fmt.Sprintf("%s -> %s", k, snake))
		}
	}

	if len(snakeCasePairs) > 0 {
		clauses = append([]string{
			fmt.Sprintf("use snake_case keys (%s)", strings.Join(snakeCasePairs, ", ")),
		}, clauses...)
	}
	if len(clauses) == 0 {
		return err
	}
	return fmt.Errorf("%w; %s", err, strings.Join(clauses, "; "))
}
