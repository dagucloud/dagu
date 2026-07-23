// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/spf13/cobra"
)

var (
	controllerFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "table",
		usage:        "Output format: table or json (default: table)",
	}
	controllerPromptFlag = commandLineFlag{
		name:     "prompt",
		usage:    "Prompt for the Controller",
		required: true,
	}
)

type controllerCommandClient interface {
	listControllers(context.Context) ([]api.ControllerSummary, error)
	getController(context.Context, string) (*api.ControllerDetail, error)
	startController(context.Context, string, string) error
	promptController(context.Context, string, string) error
	stopController(context.Context, string) error
}

// Controller returns the operational Controller command group.
func Controller() *cobra.Command {
	command := NewCommand(&cobra.Command{
		Use:   "controller",
		Short: "Inspect and control Controllers",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	command.AddCommand(controllerListCommand())
	command.AddCommand(controllerShowCommand())
	command.AddCommand(controllerStartCommand())
	command.AddCommand(controllerPromptCommand())
	command.AddCommand(controllerStopCommand())
	return command
}

func controllerListCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "list",
		Short: "List visible Controllers",
		Args:  cobra.NoArgs,
	}, []commandLineFlag{controllerFormatFlag}, runControllerList)
}

func controllerShowCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Show a Controller",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{controllerFormatFlag}, runControllerShow)
}

func controllerStartCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "start <id>",
		Short: "Start a Controller",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{controllerPromptFlag}, runControllerStart)
}

func controllerPromptCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "prompt <id>",
		Short: "Send a prompt to a waiting Controller",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{controllerPromptFlag}, runControllerPrompt)
}

func controllerStopCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a Controller",
		Args:  cobra.ExactArgs(1),
	}, nil, runControllerStop)
}

func runControllerList(ctx *Context, _ []string) error {
	format, err := controllerOutputFormat(ctx)
	if err != nil {
		return err
	}
	client := controllerClientForContext(ctx)
	controllers, err := client.listControllers(ctx)
	if err != nil {
		return err
	}
	return renderControllerList(ctx, format, controllers)
}

func runControllerShow(ctx *Context, args []string) error {
	id, err := validateControllerID(args[0])
	if err != nil {
		return err
	}
	format, err := controllerOutputFormat(ctx)
	if err != nil {
		return err
	}
	client := controllerClientForContext(ctx)
	detail, err := client.getController(ctx, id)
	if err != nil {
		return err
	}
	return renderControllerDetail(ctx, format, detail)
}

func runControllerStart(ctx *Context, args []string) error {
	id, prompt, err := controllerMutationInput(ctx, args)
	if err != nil {
		return err
	}
	client := controllerClientForContext(ctx)
	if err := client.startController(ctx, id, prompt); err != nil {
		return controllerMutationError(ctx, err)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "Start accepted for %s\n", id)
	return err
}

func runControllerPrompt(ctx *Context, args []string) error {
	id, prompt, err := controllerMutationInput(ctx, args)
	if err != nil {
		return err
	}
	client := controllerClientForContext(ctx)
	if err := client.promptController(ctx, id, prompt); err != nil {
		return controllerMutationError(ctx, err)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "Prompt accepted for %s\n", id)
	return err
}

func runControllerStop(ctx *Context, args []string) error {
	id, err := validateControllerID(args[0])
	if err != nil {
		return err
	}
	client := controllerClientForContext(ctx)
	if err := client.stopController(ctx, id); err != nil {
		return controllerMutationError(ctx, err)
	}
	_, err = fmt.Fprintf(ctx.Command.OutOrStdout(), "Stop requested for %s\n", id)
	return err
}

func controllerClientForContext(ctx *Context) controllerCommandClient {
	if ctx.IsRemote() {
		return ctx.Remote
	}
	return newLocalControllerCommandClient(ctx)
}

func controllerOutputFormat(ctx *Context) (string, error) {
	format, err := ctx.Command.Flags().GetString("format")
	if err != nil {
		return "", fmt.Errorf("read format flag: %w", err)
	}
	switch format {
	case "table", "json":
		return format, nil
	default:
		return "", fmt.Errorf("unsupported format %q; use table or json", format)
	}
}

func controllerMutationInput(ctx *Context, args []string) (string, string, error) {
	id, err := validateControllerID(args[0])
	if err != nil {
		return "", "", err
	}
	prompt, err := ctx.Command.Flags().GetString("prompt")
	if err != nil {
		return "", "", fmt.Errorf("read prompt flag: %w", err)
	}
	if err := controller.ValidatePrompt(prompt); err != nil {
		return "", "", err
	}
	return id, prompt, nil
}

func validateControllerID(id string) (string, error) {
	if err := controller.ValidateID(id); err != nil {
		return "", fmt.Errorf("invalid Controller ID %q; expected ctrl_ followed by 16 lowercase base32 characters", id)
	}
	return id, nil
}

func controllerMutationError(ctx *Context, err error) error {
	if !ctx.IsRemote() {
		return err
	}
	var responseErr *remoteError
	if errors.As(err, &responseErr) &&
		responseErr.StatusCode >= http.StatusBadRequest &&
		responseErr.StatusCode < http.StatusInternalServerError {
		return err
	}
	return fmt.Errorf("%w; outcome unknown; do not retry automatically", err)
}

type controllerListJSONItem struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Workspace         string    `json:"workspace"`
	Status            string    `json:"status"`
	CurrentState      string    `json:"currentState"`
	ResourceUpdatedAt time.Time `json:"resourceUpdatedAt"`
}

func renderControllerList(ctx *Context, format string, controllers []api.ControllerSummary) error {
	if format == "json" {
		items := make([]controllerListJSONItem, 0, len(controllers))
		for _, item := range controllers {
			items = append(items, controllerListJSONItem{
				ID:                item.Id,
				Name:              item.Name,
				Workspace:         item.Workspace,
				Status:            string(item.StatusLabel),
				CurrentState:      item.CurrentState,
				ResourceUpdatedAt: item.ResourceUpdatedAt,
			})
		}
		encoder := json.NewEncoder(ctx.Command.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(items)
	}

	w := tabwriter.NewWriter(ctx.Command.OutOrStdout(), 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "NAME\tID\tWORKSPACE\tSTATUS\tSTATE\tUPDATED"); err != nil {
		return err
	}
	for _, item := range controllers {
		workspace := item.Workspace
		if workspace == "" {
			workspace = "default"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			terminalSafeControllerText(item.Name),
			terminalSafeControllerText(item.Id),
			terminalSafeControllerText(workspace),
			terminalSafeControllerText(string(item.StatusLabel)),
			terminalSafeControllerText(item.CurrentState),
			terminalSafeControllerText(item.ResourceUpdatedAt.Format(time.RFC3339)),
		); err != nil {
			return err
		}
	}
	return w.Flush()
}

type controllerDetailJSON struct {
	ID                string                    `json:"id"`
	Name              string                    `json:"name"`
	Workspace         string                    `json:"workspace"`
	Status            string                    `json:"status"`
	CurrentState      string                    `json:"currentState"`
	TurnCount         int                       `json:"turnCount"`
	MaxTurns          int                       `json:"maxTurns"`
	WaitingQuestion   *string                   `json:"waitingQuestion"`
	ActiveDAGRun      *api.ControllerDAGRunRef  `json:"activeDAGRun"`
	DAGRuns           []api.ControllerDAGRunRef `json:"dagRuns"`
	LastError         *string                   `json:"lastError"`
	ResourceUpdatedAt time.Time                 `json:"resourceUpdatedAt"`
}

func renderControllerDetail(ctx *Context, format string, detail *api.ControllerDetail) error {
	if detail == nil {
		return fmt.Errorf("Controller response is empty")
	}
	if err := renderControllerWarnings(ctx, detail.Warnings); err != nil {
		return err
	}

	summary := controllerDetailJSON{
		ID:                detail.Id,
		Name:              detail.Definition.Name,
		Workspace:         controllerWorkspace(detail.Definition.Labels),
		Status:            string(detail.Runtime.StatusLabel),
		CurrentState:      detail.Runtime.CurrentState,
		TurnCount:         detail.Runtime.TurnCount,
		MaxTurns:          detail.Definition.MaxTurns,
		WaitingQuestion:   detail.Runtime.WaitingQuestion,
		ActiveDAGRun:      detail.Runtime.ActiveDAGRun,
		DAGRuns:           detail.Runtime.DagRunRefs,
		LastError:         detail.Runtime.LastError,
		ResourceUpdatedAt: detail.ResourceUpdatedAt,
	}
	if format == "json" {
		encoder := json.NewEncoder(ctx.Command.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(summary)
	}

	w := tabwriter.NewWriter(ctx.Command.OutOrStdout(), 0, 2, 2, ' ', 0)
	rows := [][2]string{
		{"Name", summary.Name},
		{"ID", summary.ID},
		{"Workspace", displayControllerWorkspace(summary.Workspace)},
		{"Status", summary.Status},
		{"State", summary.CurrentState},
		{"Turns", fmt.Sprintf("%d/%d", summary.TurnCount, summary.MaxTurns)},
		{"Waiting question", stringValue(summary.WaitingQuestion)},
		{"Active DAG run", formatControllerDAGRun(summary.ActiveDAGRun)},
		{"Recent DAG runs", formatControllerDAGRuns(summary.DAGRuns)},
		{"Updated", summary.ResourceUpdatedAt.Format(time.RFC3339)},
		{"Last error", stringValue(summary.LastError)},
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], terminalSafeControllerText(row[1])); err != nil {
			return err
		}
	}
	return w.Flush()
}

func renderControllerWarnings(ctx *Context, warnings []api.ControllerWarning) error {
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(
			ctx.Command.ErrOrStderr(),
			"Warning [%s] %s: %s\n",
			terminalSafeControllerText(warning.Code),
			terminalSafeControllerText(warning.Path),
			terminalSafeControllerText(warning.Message),
		); err != nil {
			return err
		}
	}
	return nil
}

func terminalSafeControllerText(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, r := range value {
		if controllerTerminalRuneNeedsEscape(r) {
			quoted := strconv.QuoteRuneToASCII(r)
			result.WriteString(quoted[1 : len(quoted)-1])
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func controllerTerminalRuneNeedsEscape(r rune) bool {
	return r < ' ' ||
		(r >= '\u007f' && r <= '\u009f') ||
		(r >= '\u2028' && r <= '\u2029') ||
		unicode.Is(unicode.Bidi_Control, r)
}

func controllerWorkspace(labels []string) string {
	workspaceName, _ := coreexec.WorkspaceNameFromLabels(core.NewLabels(labels))
	return workspaceName
}

func displayControllerWorkspace(workspace string) string {
	if workspace == "" {
		return "default"
	}
	return workspace
}

func stringValue(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}

func formatControllerDAGRun(ref *api.ControllerDAGRunRef) string {
	if ref == nil {
		return "-"
	}
	return ref.Dag + "/" + ref.DagRunId
}

func formatControllerDAGRuns(refs []api.ControllerDAGRunRef) string {
	if len(refs) == 0 {
		return "-"
	}
	values := make([]string, 0, len(refs))
	for index := range refs {
		values = append(values, formatControllerDAGRun(&refs[index]))
	}
	return strings.Join(values, ", ")
}
