// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

// Environment variable keys that are automatically set by Dagu during execution.
const (
	// EnvKeyDAGName holds the name of the currently executing DAG.
	EnvKeyDAGName = "DAG_NAME"

	// EnvKeyDAGRunID holds the unique identifier for the current DAG run.
	EnvKeyDAGRunID = "DAG_RUN_ID"

	// EnvKeyDAGRunLogFile holds the path to the main log file for the DAG run.
	EnvKeyDAGRunLogFile = "DAG_RUN_LOG_FILE"

	// EnvKeyDAGRunStepName holds the name of the currently executing step.
	EnvKeyDAGRunStepName = "DAG_RUN_STEP_NAME"

	// EnvKeyDAGRunStepStdoutFile holds the path to the stdout log file for the current step.
	EnvKeyDAGRunStepStdoutFile = "DAG_RUN_STEP_STDOUT_FILE"

	// EnvKeyDAGRunStepStderrFile holds the path to the stderr log file for the current step.
	EnvKeyDAGRunStepStderrFile = "DAG_RUN_STEP_STDERR_FILE"

	// EnvKeyDAGRunStatus holds the current status of the DAG run (e.g., "running", "success", "failed").
	EnvKeyDAGRunStatus = "DAG_RUN_STATUS"

	// EnvKeyDAGParamsJSON exposes the resolved parameters encoded as JSON.
	// When params were provided as JSON, the original payload is preserved.
	EnvKeyDAGParamsJSON = "DAGU_PARAMS_JSON"

	// EnvKeyDAGDocsDir holds the per-DAG docs directory path.
	EnvKeyDAGDocsDir = "DAG_DOCS_DIR"

	// EnvKeyDAGParamsJSONCompat is the DAG_-prefixed key for resolved parameters JSON.
	// Both DAG_PARAMS_JSON and DAGU_PARAMS_JSON are set for backward compatibility.
	EnvKeyDAGParamsJSONCompat = "DAG_PARAMS_JSON"

	// EnvKeyDAGRunWorkDir holds the path to the per-DAG-run working directory.
	EnvKeyDAGRunWorkDir = "DAG_RUN_WORK_DIR"

	// EnvKeyDAGRunArtifactsDir holds the path to the per-DAG-run artifacts directory.
	EnvKeyDAGRunArtifactsDir = "DAG_RUN_ARTIFACTS_DIR"

	// EnvKeyDAGPushBack exposes the current push-back iteration and history as JSON.
	EnvKeyDAGPushBack = "DAG_PUSHBACK"

	// EnvKeyExternalStepRetry enables parent-managed step retries for sub-DAG runs.
	// When set, retriable step failures transition to a queued retry state instead of
	// sleeping inline inside the child DAG process.
	EnvKeyExternalStepRetry = "DAGU_EXTERNAL_STEP_RETRY"

	// EnvKeyQueueDispatchRetry marks an internal retry invocation that is consuming
	// an already-queued run from the scheduler/worker queue dispatch path.
	EnvKeyQueueDispatchRetry = "DAGU_QUEUE_DISPATCH_RETRY"
)
