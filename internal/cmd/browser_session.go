// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/browser"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// maxSessionInputSize bounds the operation a session command reads from
// stdin.
const maxSessionInputSize = 1 << 20 // 1 MiB

var (
	sessionProfileFlag = commandLineFlag{
		name:  "profile",
		usage: "Keep the browser's cookies and storage under this profile name, shared with browser steps that name it",
	}
	sessionHeadedFlag = commandLineFlag{
		name:   "headed",
		isBool: true,
		usage:  "Show the browser window",
	}
	sessionViewportFlag = commandLineFlag{
		name:  "viewport",
		usage: "Size of the page area, as WIDTHxHEIGHT, such as 1280x800",
	}
	sessionExecutableFlag = commandLineFlag{
		name:  "executable",
		usage: "Path of the Chrome or Chromium to run (default: the installed Chrome)",
	}
	sessionProxyFlag = commandLineFlag{
		name:  "proxy",
		usage: "Proxy server for the browser, such as http://proxy.example.com:8080",
	}
	sessionAllowedDomainFlag = commandLineFlag{
		name:          "allowed-domain",
		isStringArray: true,
		usage:         "Keep the browser on this host or *.domain; repeat for more",
	}
	sessionWriteGateFlag = commandLineFlag{
		name:  "write-gate",
		usage: "Write gate mode: rehearse blocks and reports the page's writes, but the sign-ins --write-policy lists (default: the host's browser.write_gate)",
	}
	sessionWritePolicyFlag = commandLineFlag{
		name:  "write-policy",
		usage: "Write gate policy file naming the sign-in endpoints whose writes are sent",
	}
	sessionAllowWriteFlag = commandLineFlag{
		name:          "allow-write",
		isStringArray: true,
		usage:         "Let the operation send the write of this signature, in a session whose write gate records; repeat for more",
	}
	sessionPersonFlag = commandLineFlag{
		name:   "person",
		isBool: true,
		usage:  "A person holds the browser: send the writes they make, still reporting each",
	}
	sessionGuardFlag = commandLineFlag{
		name:   "guard",
		isBool: true,
		usage:  "Hand the browser back to the write gate's mode",
	}
	sessionIdleTimeoutFlag = commandLineFlag{
		name:         "idle-timeout",
		defaultValue: browser.DefaultSessionIdleTimeout.String(),
		usage:        "Close the browser when no command arrives for this long, up to 24h",
	}
	sessionLLMFlag = commandLineFlag{
		name:  "llm",
		usage: "Model block, as a step's llm field in YAML or JSON, such as '{provider: openai, model: gpt-5-mini}'",
	}
	sessionProviderFlag = commandLineFlag{
		name:  "provider",
		usage: "Model provider, such as openai, anthropic, gemini, openrouter, or local",
	}
	sessionModelFlag = commandLineFlag{
		name:  "model",
		usage: "Model the session's acts, extracts, and judged conditions ask",
	}
	sessionBaseURLFlag = commandLineFlag{
		name:  "base-url",
		usage: "Endpoint of the model provider, for a local or proxied model",
	}
	sessionAPIKeyNameFlag = commandLineFlag{
		name:  "api-key-name",
		usage: "Environment variable holding the provider's API key (default: the provider's usual one)",
	}
	sessionOutlineFlag = commandLineFlag{
		name:         "outline-chars",
		defaultValue: strconv.Itoa(browser.DefaultOutlineChars),
		usage:        "Most characters of the page outline to report; 0 leaves it out",
	}
	sessionFindFlag = commandLineFlag{
		name:  "find",
		usage: "Show only the outline's entries containing this text, with the entries they sit in",
	}
	sessionMaxCharsFlag = commandLineFlag{
		name:         "max-chars",
		defaultValue: strconv.Itoa(browser.DefaultDescribeChars),
		usage:        "Most characters of the outline",
	}
	sessionTreeFlag = commandLineFlag{
		name:   "tree",
		isBool: true,
		usage:  "Report the page's accessibility tree as the model sees it, in place of the outline",
	}
	sessionScreenshotFlag = commandLineFlag{
		name:  "screenshot",
		usage: "Also save a screenshot of the page under this name",
	}
	sessionDescribeFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "json",
		usage:        "Output format: json or text",
	}
	sessionDAGFlag = commandLineFlag{
		name:     "dag",
		required: true,
		usage:    "DAG the step goes in, by name or YAML file path",
	}
	sessionStepFlag = commandLineFlag{
		name:     "step",
		required: true,
		usage:    "ID of the step to build",
	}
	sessionSkipFlag = commandLineFlag{
		name:          "skip",
		isStringArray: true,
		usage:         "Leave out the operation with this index in the session's history; repeat for more",
	}
	sessionDryRunFlag = commandLineFlag{
		name:   "dry-run",
		isBool: true,
		usage:  "Build the step without writing its recordings",
	}
	sessionCloseAfterFlag = commandLineFlag{
		name:   "close",
		isBool: true,
		usage:  "Close the session after exporting it",
	}
	sessionExportFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "json",
		usage:        "Output format: json, or yaml for the step alone",
	}
	sessionForceFlag = commandLineFlag{
		name:   "force",
		isBool: true,
		usage:  "Close the session even while another command holds it",
	}
	sessionKeepFlag = commandLineFlag{
		name:   "keep",
		isBool: true,
		usage:  "End the session but keep its history for export",
	}
)

func browserSessionCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "session",
		Short: "Work a site in a browser kept open between commands",
		Long: `A browser session keeps a browser open between commands. Each command runs
one operation of a browser step's with.do on it, exactly as a step would,
and reports what happened and what the page shows next. When the operations
do what a step should, export turns them into a browser.run step whose first
run replays the session's acts instead of asking the model.

Every command prints one JSON object. A command that fails prints an object
with error.code and error.message and exits 1. Sessions are kept on this
host.

Examples:
  dagu browser session open https://portal.example.com/login --provider openai --model gpt-5-mini
  echo '{"act": "Click Sign in"}' | dagu browser session do ab2cd3ef4g
  dagu browser session describe ab2cd3ef4g --find Orders
  dagu browser session export ab2cd3ef4g --dag orders --step fetch
  dagu browser session close ab2cd3ef4g
`,
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(
		browserSessionOpenCommand(),
		browserSessionDoCommand(),
		browserSessionDescribeCommand(),
		browserSessionExportCommand(),
		browserSessionCloseCommand(),
		browserSessionListCommand(),
		browserSessionGateCommand(),
		browserSessionReapCommand(),
	)
	return cmd
}

func browserSessionGateCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "gate (--person | --guard) <session ID>",
		Short: "Tell a session's write gate who holds the browser",
		Long: `Tell the session's write gate that a person holds the browser, with --person,
or hands it back, with --guard. While a person holds it, the gate sends the
writes the page makes and still reports each with the next command.

Examples:
  dagu browser session gate --person ab2cd3ef4g
  dagu browser session gate --guard ab2cd3ef4g
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{sessionPersonFlag, sessionGuardFlag}, runBrowserSessionGate)
}

func runBrowserSessionGate(ctx *Context, args []string) error {
	flags := ctx.Command.Flags()
	person, _ := flags.GetBool(sessionPersonFlag.name)
	guard, _ := flags.GetBool(sessionGuardFlag.name)
	if person == guard {
		return writeSessionError(ctx, invalidSessionInput("give one of --person or --guard"))
	}
	result, err := browser.NewSessions().Gate(ctx, browser.GateRequest{ID: args[0], Person: person})
	if err != nil {
		return writeSessionError(ctx, err)
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), result)
}

func browserSessionOpenCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "open [flags] [URL]",
		Short: "Open a browser session",
		Long: `Open a browser, go to URL if given, and leave the browser waiting for the
session's commands. The result carries the session ID the other commands
take, the page, its outline, and the browser's DevTools address, where an
application can show the page and let a person act on it.

Give the model the session's acts, extracts, and judged conditions ask with
--llm or with --provider and --model. Without one, the session runs only
operations that need no model, such as goto and fixed expects. The model's
API key is read from the environment on every command.

With --write-gate rehearse, every write the page sends, any request but GET,
HEAD, or OPTIONS, is blocked and reported under writes, by this command and
by the next for a write the page sent while it waited. With --write-gate
record, writes are blocked the same way unless the do command that sends
one allows its signature with --allow-write; a blocked write fails the
operation with write_blocked. Sign-ins the --write-policy file lists are
sent:

  sign_in:
    - host: login.example.com
      path: /session
      secret: PORTAL_PASSWORD   # sent only when it carries this variable's value

Examples:
  dagu browser session open https://portal.example.com/login --provider openai --model gpt-5-mini
  dagu browser session open https://shop.example.com --headed --profile shop --llm '{provider: anthropic, model: claude-sonnet-4-6}'
`,
		Args: cobra.MaximumNArgs(1),
	}, []commandLineFlag{
		sessionProfileFlag, sessionHeadedFlag, sessionViewportFlag, sessionExecutableFlag, sessionProxyFlag,
		sessionAllowedDomainFlag, sessionIdleTimeoutFlag, sessionLLMFlag, sessionProviderFlag, sessionModelFlag,
		sessionBaseURLFlag, sessionAPIKeyNameFlag, sessionOutlineFlag, sessionWriteGateFlag, sessionWritePolicyFlag,
	}, runBrowserSessionOpen)
}

func runBrowserSessionOpen(ctx *Context, args []string) error {
	opts, err := sessionOpenOptions(ctx, args)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	opened, err := browser.NewSessions().Open(ctx, opts)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	if err := startSessionWatchdog(ctx, opened.ID); err != nil {
		opened.Warnings = append(opened.Warnings, "the session closes when idle only when a later session command, browser step, or server sees it: "+err.Error())
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), opened)
}

// startSessionWatchdog starts a process of its own that closes the
// session's browser once the session is idle past its timeout.
func startSessionWatchdog(ctx *Context, id string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this executable: %w", err)
	}
	args := []string{"browser", "session", "reap", id}
	// The watchdog reads the same configuration as this command.
	for _, name := range []string{configFlag.name, daguHomeFlag.name} {
		if value, _ := ctx.Command.Flags().GetString(name); value != "" {
			args = append(args, "--"+name, value)
		}
	}
	return startDetached(executable, args)
}

func browserSessionReapCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:    "reap <session ID>",
		Short:  "Close a browser session's browser once it is idle past its timeout",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
	}, nil, func(ctx *Context, args []string) error {
		return browser.NewSessions().Reap(ctx, args[0])
	})
}

func sessionOpenOptions(ctx *Context, args []string) (browser.SessionOptions, error) {
	flags := ctx.Command.Flags()
	opts := browser.SessionOptions{}
	if len(args) == 1 {
		opts.URL = args[0]
	}
	headed, _ := flags.GetBool(sessionHeadedFlag.name)
	opts.Headless = !headed
	opts.Profile, _ = flags.GetString(sessionProfileFlag.name)
	opts.Executable, _ = flags.GetString(sessionExecutableFlag.name)
	opts.Proxy, _ = flags.GetString(sessionProxyFlag.name)
	opts.AllowedDomains, _ = flags.GetStringArray(sessionAllowedDomainFlag.name)
	opts.WriteGate, _ = flags.GetString(sessionWriteGateFlag.name)
	opts.WritePolicyFile, _ = flags.GetString(sessionWritePolicyFlag.name)
	if text, _ := flags.GetString(sessionViewportFlag.name); text != "" {
		width, height, ok := strings.Cut(strings.ToLower(text), "x")
		w, wErr := strconv.Atoi(width)
		h, hErr := strconv.Atoi(height)
		if !ok || wErr != nil || hErr != nil {
			return opts, invalidSessionInput("--viewport %q must be WIDTHxHEIGHT, such as 1280x800", text)
		}
		opts.Viewport = &browser.SessionViewport{Width: w, Height: h}
	}
	idle, _ := flags.GetString(sessionIdleTimeoutFlag.name)
	timeout, err := time.ParseDuration(idle)
	if err != nil || timeout <= 0 {
		return opts, invalidSessionInput("--idle-timeout %q must be a positive duration, such as 30m", idle)
	}
	opts.IdleTimeout = timeout
	if opts.LLM, err = sessionModel(ctx); err != nil {
		return opts, err
	}
	if opts.OutlineChars, err = sessionCharsFlag(ctx, sessionOutlineFlag.name); err != nil {
		return opts, err
	}
	return opts, nil
}

// sessionModel reads the session's model from --llm, or from --provider,
// --model, --base-url, and --api-key-name.
func sessionModel(ctx *Context) (*ir.LLMConfig, error) {
	flags := ctx.Command.Flags()
	block, _ := flags.GetString(sessionLLMFlag.name)
	fields := map[string]string{}
	for name, key := range map[string]string{
		sessionProviderFlag.name:   "provider",
		sessionModelFlag.name:      "model",
		sessionBaseURLFlag.name:    "base_url",
		sessionAPIKeyNameFlag.name: "api_key_name",
	} {
		if value, _ := flags.GetString(name); value != "" {
			fields[key] = value
		}
	}
	switch {
	case block != "" && len(fields) > 0:
		return nil, invalidSessionInput("give the model with --llm or with --provider and --model, not both")
	case block != "":
		cfg, err := spec.ParseLLMConfig([]byte(block))
		if err != nil {
			return nil, invalidSessionInput("--llm: %v", err)
		}
		return cfg, nil
	case len(fields) == 0:
		return nil, nil
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	cfg, err := spec.ParseLLMConfig(data)
	if err != nil {
		return nil, invalidSessionInput("%v", err)
	}
	return cfg, nil
}

func browserSessionDoCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "do [flags] <session ID>",
		Short: "Run one operation in a browser session",
		Long: `Read one operation from stdin, in JSON or YAML, and run it in the session.
The operation is written as an item of a browser step's with.do: goto, act,
upload, extract, expect, wait, or screenshot, with when and timeout. Relative
paths an upload names are under the current directory. Give the values
of the variables an act uses under variables; they are never read from the
command line:

  {"act": "Type %password% into the Password field",
   "variables": {"password": {"env": "PORTAL_PASSWORD"}}}

A variable given as {"env": NAME} is read from the environment on this and
every later command and masked like a secret; a plain string serves this
command only.

An element the outline shows with its ID can be acted on by that ID,
without the model:

  {"click": "0-131"}
  {"type": {"into": "0-229", "text": "%user%"}}
  {"select": {"in": "0-106", "option": "未出荷"}}
  {"upload": {"into": "0-57", "files": "/path/to/invoice.pdf"}}

The session keeps each as the act a step writes, reported as act, or the
upload into the element in words, reported as into, with the action it took
as its recording.

The result reports what the operation did, the actions an act performed,
what an extract read, the writes the write gate decided, and the page the
browser is on next. An operation that fails is reported with status failed
and the command exits 1; the session stays open.

In a session whose write gate records, --allow-write lets the operation
send the write of a signature a blocked write reported; it sends no other.
A form whose write was blocked stays on the page as it was filled in, so
the same operation, allowed its write, sends it.

Examples:
  echo '{"act": "Click Sign in"}' | dagu browser session do ab2cd3ef4g
  echo '{"click": "0-131"}' | dagu browser session do ab2cd3ef4g
  echo '{"click": "0-88"}' | dagu browser session do ab2cd3ef4g --allow-write 'POST https://books.example.com/api/deals? {amount}'
  dagu browser session do ab2cd3ef4g < operation.yaml
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{sessionOutlineFlag, sessionAllowWriteFlag}, runBrowserSessionDo)
}

func runBrowserSessionDo(ctx *Context, args []string) error {
	input, err := readSessionInput(ctx.Command.InOrStdin())
	if err != nil {
		return writeSessionError(ctx, err)
	}
	op, variables, err := browser.ParseSessionInput(input)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	chars, err := sessionCharsFlag(ctx, sessionOutlineFlag.name)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	allowed, _ := ctx.Command.Flags().GetStringArray(sessionAllowWriteFlag.name)
	result, err := browser.NewSessions().Do(ctx, browser.DoRequest{ID: args[0], Operation: op, Variables: variables, OutlineChars: chars, AllowWrites: allowed})
	var sessionErr *browser.SessionError
	// An operation that ran reports its result, failed or not.
	if err != nil && (!errors.As(err, &sessionErr) || sessionErr.Code != browser.CodeOperationFailed && sessionErr.Code != browser.CodeWriteBlocked) {
		return writeSessionError(ctx, err)
	}
	if writeErr := writeIndentedJSON(ctx.Command.OutOrStdout(), result); writeErr != nil {
		return writeErr
	}
	return err
}

// readSessionInput reads the operation a command is given on stdin, which
// must not be a terminal.
func readSessionInput(in io.Reader) ([]byte, error) {
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return nil, invalidSessionInput(`give the operation on stdin, such as: echo '{"act": "Click Sign in"}' | dagu browser session do <ID>`)
	}
	data, err := io.ReadAll(io.LimitReader(in, maxSessionInputSize+1))
	if err != nil {
		return nil, fmt.Errorf("read the operation: %w", err)
	}
	if len(data) > maxSessionInputSize {
		return nil, invalidSessionInput("the operation exceeds the %d byte limit", maxSessionInputSize)
	}
	return data, nil
}

func browserSessionDescribeCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "describe [flags] <session ID>",
		Short: "Describe the page of a browser session",
		Long: `Report what the session's page shows, without asking a model: an outline of
its headings, fields with their labels, selects with their options, file
inputs, buttons, links with their addresses, messages, and tables and lists,
with long runs of alike rows collapsed. Text typed into fields is never shown.

Examples:
  dagu browser session describe ab2cd3ef4g
  dagu browser session describe ab2cd3ef4g --find "PO-1001" --format text
  dagu browser session describe ab2cd3ef4g --screenshot orders
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{sessionFindFlag, sessionMaxCharsFlag, sessionTreeFlag, sessionScreenshotFlag, sessionDescribeFormatFlag}, runBrowserSessionDescribe)
}

func runBrowserSessionDescribe(ctx *Context, args []string) error {
	flags := ctx.Command.Flags()
	format, _ := flags.GetString(sessionDescribeFormatFlag.name)
	if format != "json" && format != "text" {
		return writeSessionError(ctx, invalidSessionInput("--format %q: use json or text", format))
	}
	chars, err := sessionCharsFlag(ctx, sessionMaxCharsFlag.name)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	req := browser.DescribeRequest{ID: args[0], MaxChars: chars}
	req.Find, _ = flags.GetString(sessionFindFlag.name)
	req.Tree, _ = flags.GetBool(sessionTreeFlag.name)
	req.Screenshot, _ = flags.GetString(sessionScreenshotFlag.name)
	result, err := browser.NewSessions().Describe(ctx, req)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	out := ctx.Command.OutOrStdout()
	if format == "json" {
		return writeIndentedJSON(out, result)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\nURL: %s\n", result.Title, result.URL)
	if result.Screenshot != "" {
		fmt.Fprintf(&b, "Screenshot: %s\n", result.Screenshot)
	}
	if result.Refused != nil {
		fmt.Fprintf(&b, "Refused: %s\n", result.Refused.Message)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", warning)
	}
	b.WriteString("\n" + result.Outline + result.Tree + "\n")
	_, err = io.WriteString(out, b.String())
	return err
}

func browserSessionExportCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "export [flags] <session ID>",
		Short: "Turn a browser session into a browser.run step",
		Long: `Build a browser.run step from the operations the session ran: those that
succeeded, and those skipped because their when did not hold, in the order
they ran. Failed operations and screenshots are left out, and --skip leaves
out others by their index in the session's history.

The acts' recordings are written to the replay cache of the DAG and step
given, so the step's first run on this host replays them instead of asking
the model when it meets the pages the session met. Each variable becomes a
reference the DAG fills in: a secret for one the session read from the
environment, a parameter for a literal value.

A session whose browser has closed can still be exported for a day.

Examples:
  dagu browser session export ab2cd3ef4g --dag orders --step fetch
  dagu browser session export ab2cd3ef4g --dag orders.yaml --step fetch --skip 3 --format yaml
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{sessionDAGFlag, sessionStepFlag, sessionSkipFlag, sessionDryRunFlag, sessionCloseAfterFlag, sessionExportFormatFlag}, runBrowserSessionExport)
}

func runBrowserSessionExport(ctx *Context, args []string) error {
	flags := ctx.Command.Flags()
	format, _ := flags.GetString(sessionExportFormatFlag.name)
	if format != "json" && format != "yaml" {
		return writeSessionError(ctx, invalidSessionInput("--format %q: use json or yaml", format))
	}
	dagArg, _ := flags.GetString(sessionDAGFlag.name)
	dagName, err := extractDAGName(ctx, dagArg)
	if err != nil {
		return writeSessionError(ctx, invalidSessionInput("--dag %q: %v", dagArg, err))
	}
	req := browser.ExportRequest{ID: args[0], DAG: dagName}
	req.Step, _ = flags.GetString(sessionStepFlag.name)
	req.DryRun, _ = flags.GetBool(sessionDryRunFlag.name)
	skips, _ := flags.GetStringArray(sessionSkipFlag.name)
	for _, skip := range skips {
		index, err := strconv.Atoi(skip)
		if err != nil {
			return writeSessionError(ctx, invalidSessionInput("--skip %q must be an operation's index", skip))
		}
		req.Skip = append(req.Skip, index)
	}
	sessions := browser.NewSessions()
	result, err := sessions.Export(ctx, req)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	if closeAfter, _ := flags.GetBool(sessionCloseAfterFlag.name); closeAfter {
		if err := sessions.Close(ctx, browser.CloseRequest{ID: req.ID}); err != nil {
			result.Warnings = append(result.Warnings, "close the session: "+err.Error())
		}
	}
	out := ctx.Command.OutOrStdout()
	if format == "json" {
		return writeIndentedJSON(out, result)
	}
	step, err := result.Step.YAML()
	if err != nil {
		return err
	}
	var b strings.Builder
	if result.CacheFile != "" {
		fmt.Fprintf(&b, "# %d recorded acts written to %s\n", result.Recordings, result.CacheFile)
	}
	for _, variable := range result.Variables {
		fmt.Fprintf(&b, "# %s: %s\n", variable.Name, variable.Note)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(&b, "# warning: %s\n", warning)
	}
	b.Write(step)
	_, err = io.WriteString(out, b.String())
	return err
}

func browserSessionCloseCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "close [flags] <session ID>",
		Short: "Close a browser session",
		Long: `Close the session's browser and remove the session with its history and
files. Export the session first to keep what it did.

With --keep, the session ends instead: its browser closes and its profile
is free for a step, and its history can still be exported for a day.

Example:
  dagu browser session close ab2cd3ef4g
  dagu browser session close ab2cd3ef4g --keep
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{sessionForceFlag, sessionKeepFlag}, runBrowserSessionClose)
}

func runBrowserSessionClose(ctx *Context, args []string) error {
	flags := ctx.Command.Flags()
	force, _ := flags.GetBool(sessionForceFlag.name)
	keep, _ := flags.GetBool(sessionKeepFlag.name)
	if err := browser.NewSessions().Close(ctx, browser.CloseRequest{ID: args[0], Force: force, Keep: keep}); err != nil {
		return writeSessionError(ctx, err)
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"id": args[0], "closed": true})
}

func browserSessionListCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "list",
		Short: "List the browser sessions on this host",
		Long: `List this host's browser sessions, oldest first, after closing those idle
past their timeout. A session is idle while it waits for a command, busy
while one runs, and ended once its browser has closed; an ended session's
history can still be exported for a day.
`,
		Args: cobra.NoArgs,
	}, nil, runBrowserSessionList)
}

func runBrowserSessionList(ctx *Context, _ []string) error {
	sessions, err := browser.NewSessions().List(ctx)
	if err != nil {
		return writeSessionError(ctx, err)
	}
	return writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"sessions": sessions, "capabilities": []string{"trusted_cas", "write_gate"}})
}

func sessionCharsFlag(ctx *Context, name string) (int, error) {
	text, _ := ctx.Command.Flags().GetString(name)
	chars, err := strconv.Atoi(text)
	if err != nil || chars < 0 {
		return 0, invalidSessionInput("--%s %q must be a number of characters", name, text)
	}
	return chars, nil
}

func invalidSessionInput(format string, args ...any) error {
	return &browser.SessionError{Code: browser.CodeInvalidInput, Message: fmt.Sprintf(format, args...)}
}

// writeSessionError prints a failed command's error as JSON and returns it,
// so the command exits 1.
func writeSessionError(ctx *Context, err error) error {
	var sessionErr *browser.SessionError
	if !errors.As(err, &sessionErr) {
		sessionErr = &browser.SessionError{Code: browser.CodeFailed, Message: err.Error()}
	}
	if writeErr := writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"error": sessionErr}); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}
