// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package runenv defines environment variable keys shared across DAG run layers.
package runenv

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

	// EnvKeyDAGUOutputFile holds the file path used for declared step outputs.
	EnvKeyDAGUOutputFile = "DAGU_OUTPUT_FILE"

	// EnvKeyDAGRunStatus holds the current status of the DAG run (e.g., "running", "success", "failed").
	EnvKeyDAGRunStatus = "DAG_RUN_STATUS"

	// EnvKeyDAGWaitingSteps holds comma-separated step names that require manual action.
	EnvKeyDAGWaitingSteps = "DAG_WAITING_STEPS"

	// EnvKeyDAGParamsJSON exposes the resolved parameters encoded as JSON.
	// When params were provided as JSON, the original payload is preserved.
	EnvKeyDAGParamsJSON = "DAG_PARAMS_JSON"

	// EnvKeyDAGParamsJSONCompat exposes the resolved parameters encoded as JSON.
	EnvKeyDAGParamsJSONCompat = "DAGU_PARAMS_JSON"

	// EnvKeyDAGWikiDir holds the per-DAG Wiki directory path.
	EnvKeyDAGWikiDir = "DAG_WIKI_DIR"

	// EnvKeyDAGDocsDir is the deprecated name for EnvKeyDAGWikiDir.
	EnvKeyDAGDocsDir = "DAG_DOCS_DIR"

	// EnvKeyDAGRunWorkDir holds the path to the per-DAG-run working directory.
	EnvKeyDAGRunWorkDir = "DAG_RUN_WORK_DIR"

	// EnvKeyDAGRunArtifactsDir holds the path to the per-DAG-run artifacts directory.
	EnvKeyDAGRunArtifactsDir = "DAG_RUN_ARTIFACTS_DIR"

	// EnvKeyDAGPushBack exposes the current push-back iteration and history as JSON.
	EnvKeyDAGPushBack = "DAG_PUSHBACK"

	// EnvKeyDAGPushBackIteration exposes the current push-back iteration as a plain value.
	EnvKeyDAGPushBackIteration = "DAG_PUSHBACK_ITERATION"

	// EnvKeyDAGPushBackPreviousStdoutFile exposes the previous stdout log path for a pushed-back step.
	EnvKeyDAGPushBackPreviousStdoutFile = "DAG_PUSHBACK_PREVIOUS_STDOUT_FILE"

	// EnvKeyExternalStepRetry enables parent-managed step retries for sub-DAG runs.
	// When set, retriable step failures transition to a queued retry state instead of
	// sleeping inline inside the child DAG process.
	EnvKeyExternalStepRetry = "DAGU_EXTERNAL_STEP_RETRY"

	// EnvKeyQueueDispatchRetry marks an internal retry invocation that is consuming
	// an already-queued run from the scheduler/worker queue dispatch path.
	EnvKeyQueueDispatchRetry = "DAGU_QUEUE_DISPATCH_RETRY"
)
