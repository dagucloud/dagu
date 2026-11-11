// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"io"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentrun"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agenttools"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/resourcelimit"
	"github.com/dagucloud/dagu/v2/internal/runtime/untrusted"
)

// This file is the executor of an agent.run step (Spec 091). The step runs as
// harness.run. The executor runs the provider claude with the flags of the spec
// and reads its stream; it runs another CLI provider as harness.run does, with
// the model and the prompt only.

var _ executor.DeclaredOutputsProvider = (*agentHarness)(nil)
var _ executor.SubRunProvider = (*agentHarness)(nil)
var _ executor.ToolDefinitionProvider = (*agentHarness)(nil)
var _ executor.SkillDefinitionProvider = (*agentHarness)(nil)

// agentHarness runs an agent.run step.
type agentHarness struct {
	*harnessExecutor
	schema *agentrun.ResultSchema
	// toolCalls is the list of the successful attempt.
	toolCalls []string
	succeeded bool

	// bridge is the tool bridge of the running attempt. A stop request stops the
	// child runs of its calls.
	bridgeMu sync.Mutex
	bridge   *agenttools.Bridge
	// subRuns are the child runs of the tool calls of all attempts.
	subRuns []ir.SubDAGRun
	// toolDefs is the record of the effective catalog of the step.
	toolDefs []ir.ToolDefinition

	// skillDefs is the record of the skills of the step.
	skillDefs []ir.SkillDefinition

	// relayProtocol is the protocol of the endpoint of the step, from the start
	// check (Spec 094).
	relayProtocol aiendpoint.Protocol

	// remoteStream is the writer of the raw stream on a worker that streams its
	// logs to a coordinator. One run uses one writer for all its attempts.
	streamMu            sync.Mutex
	remoteStream        io.WriteCloser
	stopStreamFlush     func()
	streamFlushInterval time.Duration
}

// agentStreamFlushInterval is how often a run flushes the writer of the raw
// stream while the CLI runs.
const agentStreamFlushInterval = time.Second

func newAgentHarness(base *harnessExecutor, step ir.Step) (executor.Executor, error) {
	agent := &agentHarness{harnessExecutor: base}
	if step.HasOutputSchema() {
		schema, err := agentrun.NewResultSchema(step.OutputSchema)
		if err != nil {
			return nil, err
		}
		agent.schema = schema
	}
	return agent, nil
}

// Stop implements executor.Stopper. It also stops the child runs of the tool
// calls.
func (a *agentHarness) Stop(intent cmdutil.TerminationIntent) error {
	a.killBridge(intent.Signal)
	return a.harnessExecutor.Stop(intent)
}

// Kill implements executor.Executor. It also stops the child runs of the tool
// calls.
func (a *agentHarness) Kill(sig os.Signal) error {
	a.killBridge(sig)
	return a.harnessExecutor.Kill(sig)
}

// killBridge sends the signal to the child runs of the tool calls that run now.
func (a *agentHarness) killBridge(sig os.Signal) {
	a.bridgeMu.Lock()
	bridge := a.bridge
	a.bridgeMu.Unlock()
	if bridge == nil {
		return
	}
	if sig == nil {
		sig = syscall.SIGTERM
	}
	_ = bridge.Kill(sig)
}

func (a *agentHarness) setBridge(bridge *agenttools.Bridge) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	a.bridge = bridge
}

// GetSubRuns implements executor.SubRunProvider: the child runs of the calls of
// the tool bridge.
func (a *agentHarness) GetSubRuns() []ir.SubDAGRun {
	a.bridgeMu.Lock()
	runs := append([]ir.SubDAGRun(nil), a.subRuns...)
	a.bridgeMu.Unlock()
	return runs
}

// GetToolDefinitions implements executor.ToolDefinitionProvider: the effective
// catalog of the Dagu tools that the bridge served, and the entries left out.
func (a *agentHarness) GetToolDefinitions() []ir.ToolDefinition {
	return a.toolDefs
}

// GetSkillDefinitions implements executor.SkillDefinitionProvider.
func (a *agentHarness) GetSkillDefinitions() []ir.SkillDefinition {
	return a.skillDefs
}

// GetOutputs implements executor.OutputsProvider. A step that succeeded
// publishes tool_calls. A failed step publishes nothing.
func (a *agentHarness) GetOutputs() map[string]any {
	if !a.succeeded {
		return nil
	}
	calls := a.toolCalls
	if calls == nil {
		calls = []string{}
	}
	return map[string]any{ir.AgentToolCallsOutput: calls}
}

// PublishesDeclaredOutputs implements executor.DeclaredOutputsProvider.
func (a *agentHarness) PublishesDeclaredOutputs() bool { return true }

// agentInput is what the start of the step resolves for all attempts.
type agentInput struct {
	prompt string
	system string
	// servers are the MCP servers of the mcp entries, with their values
	// resolved.
	servers []claudeMCPServer
	// registry is the effective catalog of the Dagu tools for the tool bridge. It
	// is nil when the step has no Dagu tool entry.
	registry *agenttools.ToolRegistry
	// skillSet holds the skills of the step. Dagu mounts them as files.
	skillSet *agenttools.SkillSet
}

// attemptResult is what one attempt gives.
type attemptResult struct {
	kind      ir.AgentFailureKind
	reason    string
	exitCode  int
	err       error
	stdout    []byte
	toolCalls []string
}

// Run makes the attempt of the step. A retry of the step is a new execution
// that retry_policy starts.
func (a *agentHarness) Run(ctx context.Context) error {
	defer a.closeStream()
	a.succeeded = false
	a.toolCalls = nil
	a.exitCode = 0
	a.bridgeMu.Lock()
	a.subRuns = nil
	a.bridgeMu.Unlock()

	if a.step.Agent.Endpoint != "" || len(a.step.AIMCPServers()) > 0 {
		// Spec 094, "Start checks": the step fails before the CLI starts and
		// before any request goes to a provider or a MCP server.
		if err := a.checkTargets(ctx); err != nil {
			a.exitCode = 1
			return err
		}
	}

	input, err := a.resolveInput(ctx)
	if err != nil {
		a.exitCode = 1
		return err
	}
	if rootContainerConfigured(ctx) {
		a.exitCode = 1
		return errors.New("harness: agent.run does not run in the container of the DAG")
	}

	var last *executor.AgentError
	for _, cfg := range a.configs {
		failure := a.attempt(ctx, cfg, input)
		if failure == nil {
			return nil
		}
		last = failure
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				a.exitCode = 124
			}
			return last
		}
	}
	a.exitCode = last.ExitCode()
	return last
}

// resolveInput resolves the prompt, the system text and the MCP servers. A prompt has the value references of Spec 003 and no command
// substitution.
func (a *agentHarness) resolveInput(ctx context.Context) (*agentInput, error) {
	run := a.step.Agent
	prompt, err := runtime.ResolveString(ctx, run.Prompt, cmnvalue.WorkflowField("prompt"))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the prompt: %w", err)
	}
	system, err := runtime.ResolveString(ctx, run.System, cmnvalue.WorkflowField("system"))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the system text: %w", err)
	}
	input := &agentInput{prompt: prompt, system: system}
	if run.CLI == nil {
		return input, nil
	}
	input.servers, err = resolveMCPServers(ctx, run.CLI.Tools)
	if err != nil {
		return nil, err
	}
	// The catalog of the Dagu tools is built once for the step, and each attempt
	// serves it with its own bridge.
	input.registry, err = buildBridgeRegistry(ctx, run.CLI)
	if err != nil {
		return nil, fmt.Errorf("failed to build the tool catalog: %w", err)
	}
	a.toolDefs = input.registry.ToolDefinitions()
	input.skillSet = agenttools.NewSkillSet(run.CLI.Skills)
	a.skillDefs = input.skillSet.Definitions()
	return input, nil
}

// resolveMCPServers resolves the URL and the headers of each mcp entry. The
// values never enter a log or an error.
func resolveMCPServers(ctx context.Context, entries []ir.ToolEntry) ([]claudeMCPServer, error) {
	var servers []claudeMCPServer
	index := -1
	for _, entry := range entries {
		if entry.Kind != ir.ToolEntryMCP || entry.MCP == nil {
			continue
		}
		index++
		if entry.IsNamedMCP() {
			// The URL and the credentials stay in the coordinator. The CLI gets the
			// address of the relay when the attempt starts it.
			servers = append(servers, claudeMCPServer{name: entry.Name, allow: entry.MCP.Allow, named: true})
			continue
		}
		path := fmt.Sprintf("with.tools.mcp[%d]", index)
		endpoint, err := runtime.ResolveString(ctx, entry.MCP.URL, cmnvalue.WorkflowField("tools.mcp.url"))
		if err != nil {
			return nil, fmt.Errorf("%s.url: %w", path, err)
		}
		endpoint = strings.TrimSpace(endpoint)
		parsed, err := url.Parse(endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("%s.url must resolve to an http or https URL", path)
		}
		headers := make(map[string]string, len(entry.MCP.Headers))
		for key, value := range entry.MCP.Headers {
			resolved, err := runtime.ResolveString(ctx, value, cmnvalue.WorkflowField("tools.mcp.headers"))
			if err != nil {
				return nil, fmt.Errorf("%s.headers.%s: %w", path, key, err)
			}
			headers[key] = resolved
		}
		servers = append(servers, claudeMCPServer{name: entry.Name, url: endpoint, headers: headers, allow: entry.MCP.Allow})
	}
	return servers, nil
}

// attempt runs one attempt with a provider. It returns the failure of the
// attempt.
func (a *agentHarness) attempt(ctx context.Context, cfg providerConfig, input *agentInput) *executor.AgentError {
	var result attemptResult
	switch {
	case cfg.name == claudeProvider && cfg.provider != nil:
		result = a.runClaude(ctx, cfg, input)
	default:
		result = a.runOtherProvider(ctx, cfg, input)
	}

	if result.kind == ir.AgentKindOK {
		if _, err := a.stdoutWriter().Write(result.stdout); err != nil {
			a.exitCode = 1
			return executor.NewAgentError(ir.AgentKindError, 1, "stdout", err)
		}
		a.succeeded = true
		a.toolCalls = result.toolCalls
		a.exitCode = 0
		return nil
	}

	agentrun.ReportFailedAttempt(a.stderrWriter(), result.kind)
	failure := executor.NewAgentError(result.kind, result.exitCode, result.reason, result.err)
	a.exitCode = failure.ExitCode()
	return failure
}

// runOtherProvider runs a CLI provider other than claude the way harness.run
// does. It supports the model and the prompt only. The failure kind is error.
func (a *agentHarness) runOtherProvider(ctx context.Context, cfg providerConfig, input *agentInput) attemptResult {
	a.prompt = input.prompt
	spool, err := a.runOnce(ctx, cfg)
	if err != nil {
		return attemptResult{kind: ir.AgentKindError, exitCode: a.exitCode, err: err, reason: "exit"}
	}
	defer func() { _ = cleanupStdoutSpool(spool) }()
	data, readErr := io.ReadAll(spool)
	if readErr != nil {
		return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: readErr, reason: "stdout"}
	}
	return attemptResult{kind: ir.AgentKindOK, stdout: data}
}

// reasonEffortInvalid is the reason of an attempt whose key effort, after the
// resolution of its references, is not one of the levels of the CLI.
const reasonEffortInvalid = "effort_invalid"

// resolvedEffort returns the key effort of the step after resolution. It is empty
// when the step has no key. The value never changes: a value outside the levels
// of the CLI is an error. The harness does not check that the model supports the level.
func resolvedEffort(flags map[string]any) (string, error) {
	raw, present := flags["effort"]
	if !present {
		return "", nil
	}
	effort, _ := raw.(string)
	if !slices.Contains(ir.AgentEffortLevels, effort) {
		return "", fmt.Errorf("effort must be one of %s, and the value is %q", strings.Join(ir.AgentEffortLevels, ", "), fmt.Sprint(raw))
	}
	return effort, nil
}

// runClaude runs one attempt with the provider claude: the command of the spec,
// the stream as the source of the result and the failure kind.
func (a *agentHarness) runClaude(ctx context.Context, cfg providerConfig, input *agentInput) attemptResult {
	run := a.step.Agent
	cli := run.CLI
	if cli == nil {
		cli = &ir.AgentCLIConfig{}
	}
	effort, err := resolvedEffort(cfg.flags)
	if err != nil {
		return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: reasonEffortInvalid}
	}

	// The tool bridge lives for this attempt only. Its secret is in the MCP config
	// and nowhere else.
	servers := input.servers
	var bridge *agenttools.Bridge
	var secrets []string
	if input.registry.HasTools() {
		started, err := a.startBridge(ctx, input)
		if err != nil {
			return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: reasonBridgeUnavailable}
		}
		bridge = started
		a.setBridge(bridge)
		defer func() {
			a.setBridge(nil)
			_ = bridge.Close()
			a.keepSubRuns(bridge.SubRuns())
		}()
		servers = append([]claudeMCPServer{bridgeServer(bridge)}, servers...)
		secrets = append(secrets, bridge.Secret())
	}

	// The relay of the gateway lives for this attempt only (Spec 094). It serves the
	// endpoint of the step and its named MCP servers. Its secret is the placeholder
	// key of the CLI and the bearer token of the MCP config. It never enters a log.
	var relay *aigateway.Relay
	if run.Endpoint != "" || len(a.step.AIMCPServers()) > 0 {
		started, err := a.startRelay(ctx)
		if err != nil {
			return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: reasonRelayUnavailable}
		}
		relay = started
		defer func() { _ = relay.Close() }()
		secrets = append(secrets, relay.Secret())
		servers = withRelayServers(servers, relay)
	}

	configPath, cleanupConfig, err := attemptMCPConfig(ctx, servers)
	if err != nil {
		return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: "mcp_config"}
	}
	defer cleanupConfig()

	mounted := input.skillSet.Loadable()

	// A skill is a file in the agent directory of the attempt.
	agentDir, removeAgentDir, err := materializeSkills(mounted)
	if err != nil {
		return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: "skills_mount"}
	}
	defer removeAgentDir()
	// The restricted user reads the skills and cannot write them.
	if agentDir != "" {
		if err := untrusted.FromContext(ctx).ShareReadOnly(agentDir); err != nil {
			return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: "skills_mount"}
		}
	}

	tools := deriveClaudeTools(cli.Tools, servers, len(mounted) > 0)
	invocation := claudeInvocation{
		prompt:         input.prompt,
		permissionMode: claudePermissionMode,
		appendSystem:   appendSystemText(input.system),
		tools:          tools.names,
		allow:          tools.allow,
		deny:           tools.deny,
		mcpConfig:      configPath,
		addDir:         agentDir,
		maxTurns:       run.MaxToolIterations,
	}
	invocation.model, _ = cfg.flags["model"].(string)
	invocation.effort = effort
	if a.step.HasOutputSchema() {
		data, err := json.Marshal(a.step.OutputSchema)
		if err != nil {
			return attemptResult{kind: ir.AgentKindError, exitCode: 1, err: err, reason: "json_schema"}
		}
		invocation.jsonSchema = string(data)
	}

	stream := &claudeStream{}
	var writers = []io.Writer{stream}
	masker := secretMasker(streamSecrets(ctx, secrets))
	if sink, release := a.openStreamSink(ctx); sink != nil {
		// The stream keeps the text of the CLI, but for the secrets of the attempt
		// and of the run, which never enter a log file.
		masked := masking.NewMaskingWriter(sink, masker)
		defer func() {
			_ = masked.Flush()
			release()
		}()
		writers = append(writers, masked)
	}

	exitCode, runErr := a.runClaudeProcess(ctx, cfg, invocation.args(), io.MultiWriter(writers...), masker, relay)
	stream.Flush()
	if ctx.Err() != nil {
		return attemptResult{kind: ir.AgentKindError, exitCode: exitCode, err: errors.Join(ctx.Err(), runErr), reason: "interrupted"}
	}

	outcome := classifyClaude(exitCode, stream)
	result := attemptResult{
		kind:      outcome.kind,
		reason:    outcome.reason,
		exitCode:  outcome.exitCode,
		toolCalls: stream.toolCalls,
	}
	// The CLI reports the servers that it reached in the event system/init. A
	// bridge that it did not reach fails the attempt. An error of the login or of
	// the rate limit keeps its kind, because it says what the author must do.
	if bridge != nil && outcome.kind != ir.AgentKindAuthRequired && outcome.kind != ir.AgentKindRateLimited {
		if answered, reached := bridgeReached(stream); answered && !reached {
			result.kind = ir.AgentKindError
			result.reason = reasonBridgeUnavailable
			result.exitCode = max(exitCode, 1)
			result.err = fmt.Errorf("the CLI did not connect to the tool bridge (status %q)", stream.mcpServers[agenttools.BridgeServerName])
			return result
		}
	}
	if outcome.kind != ir.AgentKindOK {
		result.err = errors.New(firstNonEmpty(outcome.detail, "the attempt did not succeed"))
		if refusal := relayRefusal(relay); refusal != nil {
			// The relay or the coordinator refused a request: the reason of the
			// refusal tells the author what to change (Spec 094, "Failure kinds").
			result.reason = string(refusal.Reason)
			result.err = fmt.Errorf("%w; the AI relay refused a request: %s", result.err, refusal)
		}
		if runErr != nil && !errors.Is(runErr, io.EOF) {
			result.err = fmt.Errorf("%s: %w", result.err, runErr)
		}
		return result
	}

	stdout, resultErr := a.typedStdout(stream)
	if resultErr != nil {
		result.kind = ir.AgentKindError
		result.reason = string(resultErr.Reason)
		result.exitCode = 1
		result.err = resultErr
		return result
	}
	result.stdout = stdout
	return result
}

// typedStdout returns the text that the step writes to stdout for a successful
// attempt: the typed result as one line of JSON, or the text of the result
// message. Dagu never reads JSON from the text of the result.
func (a *agentHarness) typedStdout(stream *claudeStream) ([]byte, *agentrun.ResultError) {
	result := stream.result
	if a.schema != nil {
		raw := []byte(result.StructuredOutput)
		if len(raw) == 0 || string(raw) == "null" {
			return nil, &agentrun.ResultError{Reason: agentrun.ReasonNoResult, Err: errors.New("the result message has no structured_output")}
		}
		line, err := a.schema.Check(raw)
		if err != nil {
			return nil, err
		}
		return append(line, '\n'), nil
	}
	if result.Result == nil || *result.Result == "" {
		return nil, nil
	}
	return []byte(*result.Result + "\n"), nil
}

// streamFilePath returns the path of the file for the raw stream: the path of
// the stdout log with the suffix .stream.jsonl. It is empty when the step has no
// stdout log.
func streamFilePath(ctx context.Context) string {
	stdout := runtime.AllEnvsMap(ctx)[runenv.EnvKeyDAGRunStepStdoutFile]
	if stdout == "" {
		return ""
	}
	return stdout + ir.AgentStreamSuffix
}

// openStreamSink returns where the raw stream of an attempt goes, and the
// function that releases the sink at the end of the attempt. The sink is nil
// when the step has no place for a stream.
//
// On a worker that streams its logs to a coordinator, the sink is the log writer
// of the type agent stream. The coordinator stores it next to the stdout log. A
// local run writes the file next to the stdout log.
func (a *agentHarness) openStreamSink(ctx context.Context) (io.Writer, func()) {
	if factory := runctx.GetContext(ctx).LogWriterFactory; factory != nil {
		return a.remoteStreamWriter(ctx, factory), func() {}
	}
	path := streamFilePath(ctx)
	if path == "" {
		return nil, func() {}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- the path is the stdout log path of the step.
	if err != nil {
		logger.Warn(ctx, "Failed to open the stream file of the agent", tag.Error(err))
		return nil, func() {}
	}
	return file, func() { _ = file.Close() }
}

// remoteStreamWriter returns the log writer of the raw stream. It opens the
// writer at the first call of a run, because a new writer starts at the start
// of the file of the coordinator. While the run is open, a goroutine flushes
// the writer, as the node does for the stdout log.
func (a *agentHarness) remoteStreamWriter(ctx context.Context, factory runctx.LogWriterFactory) io.Writer {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	if a.remoteStream != nil {
		return a.remoteStream
	}
	// The delivery of the last bytes ends with the run, also for a run that Dagu
	// stops. The writer bounds its own retries.
	writer := factory.NewStepWriter(context.WithoutCancel(ctx), a.step.Name, runctx.StreamTypeAgentStream)
	a.remoteStream = writer

	flusher, ok := writer.(interface{ FlushIfDue() error })
	if !ok {
		return writer
	}
	interval := a.streamFlushInterval
	if interval <= 0 {
		interval = agentStreamFlushInterval
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// A failed flush keeps its bytes for the next one.
				_ = flusher.FlushIfDue()
			}
		}
	}()
	a.stopStreamFlush = func() {
		close(stop)
		<-done
	}
	return writer
}

// closeStream ends the log writer of the raw stream at the end of a run. The
// writer sends its last bytes and its end marker.
func (a *agentHarness) closeStream() {
	a.streamMu.Lock()
	writer, stop := a.remoteStream, a.stopStreamFlush
	a.remoteStream, a.stopStreamFlush = nil, nil
	a.streamMu.Unlock()
	if stop != nil {
		stop()
	}
	if writer != nil {
		_ = writer.Close()
	}
}

// keepSubRuns adds the child runs of the calls of a bridge to the record of the
// step.
func (a *agentHarness) keepSubRuns(runs []ir.SubDAGRun) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	a.subRuns = append(a.subRuns, runs...)
}

// writeMCPConfig writes the MCP config of the attempt to a file that only the
// owner can read, and returns its path and the function that deletes it. Dagu
// deletes the file at the end of the attempt.
func writeMCPConfig(servers []claudeMCPServer) (string, func(), error) {
	content, err := claudeMCPConfig(servers)
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to encode the MCP config: %w", err)
	}
	file, err := os.CreateTemp("", "dagu-agent-mcp-*.json")
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to create the MCP config: %w", err)
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("failed to write the MCP config: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("failed to write the MCP config: %w", err)
	}
	if err := os.Chmod(file.Name(), 0o400); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("failed to restrict the MCP config: %w", err)
	}
	return file.Name(), cleanup, nil
}

// attemptMCPConfig writes the MCP config of the attempt. For an untrusted step
// with isolation, the file belongs to the restricted user and has the mode 0400
// (Spec 092, "Files of the attempt"). Dagu deletes the file at the end of the
// attempt.
func attemptMCPConfig(ctx context.Context, servers []claudeMCPServer) (string, func(), error) {
	step := untrusted.FromContext(ctx)
	if !step.Isolated() {
		return writeMCPConfig(servers)
	}
	content, err := claudeMCPConfig(servers)
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to encode the MCP config: %w", err)
	}
	path, err := step.WriteFile("mcp.json", content)
	if err != nil {
		return "", func() {}, fmt.Errorf("failed to write the MCP config: %w", err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// runClaudeProcess starts claude on the host and waits for it. The stdout of the
// process goes to out. It returns the exit code of the process.
func (a *agentHarness) runClaudeProcess(ctx context.Context, cfg providerConfig, args []string, out io.Writer, masker *masking.Masker, relay *aigateway.Relay) (int, error) {
	a.mu.Lock()
	env := runtime.GetEnv(ctx)
	tw := executor.NewTailWriterWithEncoding(a.stderrWriter(), 0, env.LogEncodingCharset)
	a.stderrTail = tw

	// The secret of the bridge never enters the log of the step, nor the tail
	// that an error quotes.
	var stderr io.Writer = tw
	if masker != nil {
		masked := masking.NewMaskingWriter(tw, masker)
		defer func() { _ = masked.Flush() }()
		stderr = masked
	}

	cmd, err := a.invocationCommand(ctx, cfg, args, nil, out, stderr)
	if err != nil {
		a.mu.Unlock()
		return exitCodeFromError(err), err
	}
	if relay != nil {
		if a.step.Agent.Endpoint != "" {
			// The CLI calls the relay with the relay secret, and no model route of
			// the deployment applies (Spec 094, "Client settings").
			cmd.Env = relayEnvironment(cmd.Env, relay.URL(), relay.Secret())
		} else {
			// A step with named MCP servers only keeps its model variables.
			cmd.Env = loopbackNoProxy(cmd.Env)
		}
	}
	if cmd.Dir != "" {
		if err := os.MkdirAll(cmd.Dir, 0o750); err != nil {
			a.mu.Unlock()
			return 1, fmt.Errorf("harness: failed to create working directory: %w", err)
		}
	}
	process, err := untrusted.StartManagedProcess(ctx, cmd)
	if err != nil {
		a.mu.Unlock()
		return exitCodeFromError(err), formatProcessFailure(err, tw.Tail(), "")
	}
	a.process = process
	if guard := resourcelimit.FromContext(ctx); guard != nil {
		if err := guard.AssignProcess(process.PID()); err != nil {
			logger.Warn(ctx, "Resource limits requested but process assignment failed", tag.Error(err))
		}
	}
	a.mu.Unlock()
	defer func() {
		// A stop request after the end of the process has nothing to stop.
		a.mu.Lock()
		a.process = nil
		a.mu.Unlock()
		_ = process.Release()
	}()

	waitDone := make(chan error, 1)
	go func() { waitDone <- process.Wait() }()

	select {
	case <-ctx.Done():
		_ = a.stop(cmdutil.StopRequest{Intent: cmdutil.ForceTermination(), Reason: cmdutil.StopReasonTimeout})
		<-waitDone
		return 124, ctx.Err()
	case err := <-waitDone:
		return exitCodeFromError(err), err
	}
}
