// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
)

const (
	backfillTimeLayout = "2006-01-02 15:04"
	// backfillPlanEdgeRows is how many slots the text plan shows at each end.
	backfillPlanEdgeRows = 3
	backfillFormatText   = "text"
	backfillFormatJSON   = "json"
)

var (
	backfillFromFlag = commandLineFlag{
		name:  "from",
		usage: "First slot, inclusive: a date, a time (2006-01-02T15:04), or RFC 3339",
	}
	backfillToFlag = commandLineFlag{
		name:  "to",
		usage: "Last slot, inclusive (default: now)",
	}
	backfillSlotFlag = commandLineFlag{
		name:          "slot",
		usage:         "A slot to run; repeatable. Excludes --from, --to, and --rerun",
		isStringArray: true,
	}
	backfillRerunFlag = commandLineFlag{
		name:         "rerun",
		usage:        "Slots that already have a run to run again: none, failed, or all",
		defaultValue: string(backfill.RerunNone),
	}
	backfillMaxConcurrencyFlag = commandLineFlag{
		name:         "max-concurrency",
		usage:        "Runs of this backfill allowed at once",
		defaultValue: "1",
	}
	backfillOnFailureFlag = commandLineFlag{
		name:         "on-failure",
		usage:        "What to do after a slot fails: stop or continue",
		defaultValue: string(backfill.OnFailureStop),
	}
	backfillParamsFlag = commandLineFlag{
		name:  "params",
		usage: "Params for every run (default: the DAG's defaults)",
	}
	backfillDryRunFlag = commandLineFlag{
		name:   "dry-run",
		usage:  "Print the plan and start nothing",
		isBool: true,
	}
	backfillFormatFlag = commandLineFlag{
		name:         "format",
		usage:        "Plan output: text or json",
		defaultValue: backfillFormatText,
	}
	backfillYesFlag = commandLineFlag{
		name:      "yes",
		shorthand: "y",
		usage:     "Start without asking",
		isBool:    true,
	}
)

// Backfill creates the 'backfill' command group.
func Backfill() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "backfill",
		Short: "Run a scheduled DAG for past schedule slots",
		Long: `A backfill runs a DAG once for each past slot of its schedule that needs
a run, oldest first. Each run gets its slot's scheduled time and window.
The scheduler starts the runs through the DAG's queue.`,
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(backfillStartCommand())
	cmd.AddCommand(backfillLsCommand())
	cmd.AddCommand(backfillStatusCommand())
	return cmd
}

func backfillLsCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "ls [DAG]",
		Short: "List backfills",
		Long:  `List the backfills of every DAG, or of one, oldest first.`,
		Args:  cobra.MaximumNArgs(1),
	}, nil, runBackfillLs)
}

func runBackfillLs(ctx *Context, args []string) error {
	dagName := ""
	if len(args) == 1 {
		dagName = args[0]
	}
	records, err := ctx.Persistence.BackfillStore.List(ctx, dagName)
	if err != nil {
		return err
	}
	out := ctx.Command.OutOrStdout()
	if len(records) == 0 {
		_, err := fmt.Fprintln(out, "No backfills.")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tDAG\tSTATUS\tSLOTS\tSUBMITTED")
	for _, record := range records {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", record.ID, record.DAGName, backfillStatusLabel(ctx, record),
			len(record.Slots), record.CreatedAt.In(ctx.Config.Core.Location).Format(backfillTimeLayout))
	}
	return w.Flush()
}

func backfillStatusCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "status <backfill id>",
		Short: "Show where a backfill stands",
		Long: `Show a backfill's status and the state of each of its slots: pending,
queued, running, waiting, succeeded, failed, skipped, or not run.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{backfillFormatFlag}, runBackfillStatus)
}

func runBackfillStatus(ctx *Context, args []string) error {
	record, err := ctx.Persistence.BackfillStore.Get(ctx, args[0])
	if errors.Is(err, backfill.ErrNotFound) {
		return fmt.Errorf("backfill %q not found", args[0])
	}
	if err != nil {
		return err
	}
	report, err := backfill.Describe(ctx, ctx.Persistence.BackfillStore, latestRunStatus(ctx.Persistence.DAGRunRepository), record)
	if err != nil {
		return err
	}
	format, _ := ctx.Command.Flags().GetString("format")
	out := ctx.Command.OutOrStdout()
	if format == backfillFormatJSON {
		return json.NewEncoder(out).Encode(map[string]any{
			"id":     record.ID,
			"dag":    record.DAGName,
			"status": backfillStatusLabel(ctx, record),
			"counts": report.Counts,
			"slots":  report.Slots,
		})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Backfill %s of %s: %s\n", record.ID, record.DAGName, backfillStatusLabel(ctx, record))
	var parts []string
	for _, state := range backfill.SlotStateOrder {
		if n := report.Counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", state, n))
		}
	}
	fmt.Fprintf(&b, "%d slots: %s\n\n", len(report.Slots), strings.Join(parts, ", "))
	if _, err := io.WriteString(out, b.String()); err != nil {
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SLOT\tSTATE\tRUN ID")
	for _, slot := range report.Slots {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", formatBackfillTime(slot.At.In(ctx.Config.Core.Location)), slot.State, slot.RunID)
	}
	return w.Flush()
}

// latestRunStatus reads the latest status of a run.
func latestRunStatus(repository *persis.DAGRunRepository) func(context.Context, ir.DAGRunRef) (*ir.DAGRunStatus, error) {
	return func(ctx context.Context, ref ir.DAGRunRef) (*ir.DAGRunStatus, error) {
		attempt, err := repository.FindAttempt(ctx, ref)
		if err != nil {
			return nil, err
		}
		return attempt.ReadStatus(ctx)
	}
}

// backfillStatusLabel names how a backfill ended, or that it is open, and
// whether it waits for a scheduler.
func backfillStatusLabel(ctx *Context, record *backfill.Record) string {
	if !record.Open() {
		return string(record.Status)
	}
	if !schedulerRunning(ctx) {
		return "waiting for scheduler"
	}
	return "running"
}

func backfillStartCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "start [flags] <DAG>",
		Short: "Plan and start a backfill",
		Long: `Plan a backfill over a range of slots, or the slots named with --slot,
show the plan, and start it once confirmed.

A date for --from means the start of that day, and for --to the end of it.
Dates and times without an offset are read in the schedule's time zone.
--to defaults to now, and a later --to is clamped to now.

Examples:
  dagu backfill start etl --from 2026-10-02 --to 2026-11-01
  dagu backfill start etl --from 2026-10-02 --rerun failed --dry-run
  dagu backfill start etl --slot 2026-10-05T02:00 --slot 2026-10-09T02:00 --yes
`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{
		backfillFromFlag, backfillToFlag, backfillSlotFlag, backfillRerunFlag,
		backfillMaxConcurrencyFlag, backfillOnFailureFlag, backfillParamsFlag, profileFlag,
		backfillDryRunFlag, backfillFormatFlag, backfillYesFlag,
	}, runBackfillStart)
}

// backfillStartOptions are the parsed flags of 'backfill start'.
type backfillStartOptions struct {
	from, to  string
	slots     []string
	rerun     backfill.Rerun
	options   backfill.Options
	dryRun    bool
	yes       bool
	format    string
	rerunSet  bool
	rangeSet  bool
	slotNamed bool
}

func runBackfillStart(ctx *Context, args []string) error {
	opts, err := parseBackfillStartFlags(ctx)
	if err != nil {
		return err
	}
	if !opts.dryRun && !ctx.Config.Queues.Enabled {
		return errors.New("a backfill runs through the DAG's queue; set queues.enabled to true")
	}
	if !opts.dryRun && !opts.yes && (opts.format == backfillFormatJSON || !isTerminal(os.Stdin)) {
		return errors.New("no terminal to confirm the backfill on; pass --yes to start it")
	}

	dag, err := loadBackfillDAG(ctx, args[0])
	if err != nil {
		return err
	}
	if len(dag.Schedule) == 0 {
		return fmt.Errorf("DAG %q has no schedule to backfill", dag.Name)
	}
	sched := backfill.SlotSchedule{Schedules: dag.Schedule, Profile: opts.options.Profile, Location: ctx.Config.Core.Location}
	planReq, err := backfillPlanRequest(ctx, dag, sched, opts)
	if err != nil {
		return err
	}
	// The range as given identifies the backfill; To stays zero when it
	// defaulted to now.
	opts.options.From, opts.options.To, opts.options.Slots = planReq.From, planReq.To, planReq.Slots
	service := &backfill.Service{Store: ctx.Persistence.BackfillStore, History: ctx.Persistence.DAGRunRepository}
	submit := backfill.SubmitRequest{Plan: planReq, Options: opts.options, DAGFile: dag.Location, DryRun: true}

	// The plan is shown first and submitted with its revision, so the
	// backfill that starts is the one confirmed.
	shown, err := service.Submit(ctx, submit)
	if err != nil {
		return err
	}
	out := ctx.Command.OutOrStdout()
	if shown.Existing && !opts.dryRun {
		return reportBackfillSubmit(out, opts.format, shown)
	}
	warnings := backfillWarnings(ctx, dag, shown.Plan)
	if opts.format == backfillFormatJSON {
		if opts.dryRun {
			return json.NewEncoder(out).Encode(shown.Plan)
		}
	} else if err := renderBackfillPlan(out, shown.Plan, opts.options, warnings); err != nil {
		return err
	}
	if opts.dryRun {
		return nil
	}
	runs := len(shown.Plan.Runs())
	if runs == 0 {
		_, err := fmt.Fprintln(out, "Nothing to run.")
		return err
	}
	if !opts.yes && !confirmAction(fmt.Sprintf("Start %s?", countRuns(runs))) {
		_, err := fmt.Fprintln(out, "Cancelled.")
		return err
	}

	submit.DryRun = false
	submit.Revision = shown.Plan.Revision
	result, err := service.Submit(ctx, submit)
	if errors.Is(err, backfill.ErrStalePlan) {
		return errors.New("the plan changed while it was shown; run the command again to see the new plan")
	}
	if err != nil {
		return err
	}
	return reportBackfillSubmit(out, opts.format, result)
}

func parseBackfillStartFlags(ctx *Context) (backfillStartOptions, error) {
	flags := ctx.Command.Flags()
	var opts backfillStartOptions
	var err error
	if opts.from, err = flags.GetString("from"); err != nil {
		return opts, err
	}
	if opts.to, err = flags.GetString("to"); err != nil {
		return opts, err
	}
	if opts.slots, err = flags.GetStringArray("slot"); err != nil {
		return opts, err
	}
	opts.rangeSet = opts.from != "" || opts.to != ""
	opts.slotNamed = len(opts.slots) > 0
	opts.rerunSet = flags.Changed("rerun")
	switch {
	case opts.slotNamed && (opts.rangeSet || opts.rerunSet):
		return opts, errors.New("--slot cannot be combined with --from, --to, or --rerun")
	case !opts.slotNamed && opts.from == "":
		return opts, errors.New("give --from, or name slots with --slot")
	}

	rerun, _ := flags.GetString("rerun")
	if opts.rerun, err = backfill.ParseRerun(rerun); err != nil {
		return opts, err
	}
	concurrency, _ := flags.GetString("max-concurrency")
	maxConcurrency, err := strconv.Atoi(concurrency)
	if err != nil || maxConcurrency < 1 {
		return opts, fmt.Errorf("--max-concurrency must be a positive integer")
	}
	onFailureValue, _ := flags.GetString("on-failure")
	onFailure, err := backfill.ParseOnFailure(onFailureValue)
	if err != nil {
		return opts, err
	}
	params, _ := flags.GetString("params")
	profile, err := runtimeProfileNameParam(ctx)
	if err != nil {
		return opts, err
	}
	if opts.dryRun, err = flags.GetBool("dry-run"); err != nil {
		return opts, err
	}
	if opts.yes, err = flags.GetBool("yes"); err != nil {
		return opts, err
	}
	opts.format, _ = flags.GetString("format")
	if opts.format != backfillFormatText && opts.format != backfillFormatJSON {
		return opts, fmt.Errorf("invalid --format %q: use text or json", opts.format)
	}
	opts.options = backfill.Options{
		Rerun:          opts.rerun,
		MaxConcurrency: maxConcurrency,
		OnFailure:      onFailure,
		Params:         params,
		Profile:        profile,
	}
	return opts, nil
}

func loadBackfillDAG(ctx *Context, name string) (*ir.DAG, error) {
	loadOpts := []spec.LoadOption{
		spec.WithoutEval(),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
		spec.WithCalendarsDir(ctx.Config.Paths.CalendarsDir),
	}
	if ctx.Config.Paths.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(ctx.Config.Paths.BaseConfig))
	}
	return spec.Load(ctx, name, loadOpts...)
}

// backfillPlanRequest resolves the range or the named slots of a submit.
func backfillPlanRequest(ctx *Context, dag *ir.DAG, sched backfill.SlotSchedule, opts backfillStartOptions) (backfill.PlanRequest, error) {
	req := backfill.PlanRequest{
		DAG:      dag,
		Schedule: sched,
		Rerun:    opts.rerun,
		MaxSlots: ctx.Config.Scheduler.BackfillMaxSlots,
		Now:      time.Now(),
	}
	if opts.slotNamed {
		for _, value := range opts.slots {
			slot, err := ir.ResolveScheduleSlot(ir.ScheduleSlotRequest{
				Value:     value,
				Schedules: sched.Schedules,
				Profile:   sched.Profile,
				Location:  sched.Location,
				Now:       req.Now,
			})
			if err != nil {
				return req, fmt.Errorf("--slot %s: %w", value, err)
			}
			if slot.Warning != "" {
				return req, fmt.Errorf("--slot %s: %s", value, slot.Warning)
			}
			req.Slots = append(req.Slots, slot.At)
		}
		return req, nil
	}
	from, err := ir.ResolveRangeBound(opts.from, sched.Schedules, sched.Profile, sched.Location, ir.RangeStart)
	if err != nil {
		return req, fmt.Errorf("--from: %w", err)
	}
	req.From = from
	if opts.to != "" {
		if req.To, err = ir.ResolveRangeBound(opts.to, sched.Schedules, sched.Profile, sched.Location, ir.RangeEnd); err != nil {
			return req, fmt.Errorf("--to: %w", err)
		}
	}
	return req, nil
}

// backfillWarnings lists what the operator should know before starting.
func backfillWarnings(ctx *Context, dag *ir.DAG, plan backfill.Plan) []string {
	var warnings []string
	if !schedulerRunning(ctx) {
		warnings = append(warnings, "No scheduler is running; the backfill starts when dagu scheduler or dagu start-all runs.")
	}
	if suspended, err := ctx.Persistence.DAGRepository.IsSuspended(ctx, dag.SuspendFlagName()); err == nil && suspended {
		warnings = append(warnings, "The DAG is suspended; its backfill runs still run.")
	}
	if handlers := handlerNames(dag); len(handlers) > 0 {
		warnings = append(warnings, fmt.Sprintf("handler_on steps run once per slot: %s.", strings.Join(handlers, ", ")))
	}
	if plan.OtherCadence > 0 {
		warnings = append(warnings, fmt.Sprintf("%d slots run because the schedule's cadence changed since their runs.", plan.OtherCadence))
	}
	return warnings
}

func schedulerRunning(ctx *Context) bool {
	if ctx.Persistence.ServiceRegistry == nil {
		return false
	}
	members, err := ctx.Persistence.ServiceRegistry.GetServiceMembers(ctx, serviceregistry.ServiceNameScheduler)
	if err != nil {
		return false
	}
	for _, member := range members {
		if member.Status == serviceregistry.ServiceStatusActive {
			return true
		}
	}
	return false
}

func handlerNames(dag *ir.DAG) []string {
	var names []string
	for _, step := range []*ir.Step{dag.HandlerOn.Init, dag.HandlerOn.Success, dag.HandlerOn.Failure, dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait} {
		if step != nil {
			names = append(names, step.Name)
		}
	}
	return names
}

func renderBackfillPlan(out io.Writer, plan backfill.Plan, options backfill.Options, warnings []string) error {
	var b strings.Builder
	zone := ""
	if len(plan.Slots) > 0 {
		zone = ", " + plan.Slots[0].Window.Timezone
	}
	fmt.Fprintf(&b, "Backfill %s%s, --rerun %s\n\n", plan.DAGName, zone, plan.Rerun)
	if len(plan.Slots) == 0 {
		b.WriteString("No slots in the range.\n")
		_, err := io.WriteString(out, b.String())
		return err
	}
	first, last := plan.Slots[0], plan.Slots[len(plan.Slots)-1]
	fmt.Fprintf(&b, "Fires       %s to %s\n", formatBackfillTime(first.At), formatBackfillTime(last.At))
	if !first.Window.Start.IsZero() {
		fmt.Fprintf(&b, "Windows     %s to %s\n", formatBackfillTime(first.Window.Start.In(first.At.Location())), formatBackfillTime(last.At))
	}
	if plan.Clamped {
		fmt.Fprintf(&b, "--to clamped to now, %s\n", formatBackfillTime(plan.To.In(first.At.Location())))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%d slots: %s\n", len(plan.Slots), backfillActionCounts(plan))
	fmt.Fprintf(&b, "Concurrency %d, on failure %s\n\n", options.MaxConcurrency, options.OnFailure)
	if _, err := io.WriteString(out, b.String()); err != nil {
		return err
	}

	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SLOT\tWINDOW START\tACTION")
	for i, slot := range plan.Slots {
		if len(plan.Slots) > 2*backfillPlanEdgeRows && i == backfillPlanEdgeRows {
			_, _ = fmt.Fprintln(w, "...\t\t")
		}
		if len(plan.Slots) > 2*backfillPlanEdgeRows && i >= backfillPlanEdgeRows && i < len(plan.Slots)-backfillPlanEdgeRows {
			continue
		}
		windowStart := ""
		if !slot.Window.Start.IsZero() {
			windowStart = formatBackfillTime(slot.Window.Start.In(slot.At.Location()))
		}
		action := string(slot.Action)
		if !slot.CoveredAt.IsZero() {
			action += fmt.Sprintf(" (run of %s)", formatBackfillTime(slot.CoveredAt.In(slot.At.Location())))
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", formatBackfillTime(slot.At), windowStart, action)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, warning := range warnings {
		if _, err := fmt.Fprintf(out, "\nWarning: %s", warning); err != nil {
			return err
		}
	}
	if len(warnings) > 0 {
		_, err := fmt.Fprintln(out)
		return err
	}
	return nil
}

func backfillActionCounts(plan backfill.Plan) string {
	counts := map[backfill.Action]int{}
	for _, slot := range plan.Slots {
		counts[slot.Action]++
	}
	var parts []string
	for _, action := range []backfill.Action{backfill.ActionRun, backfill.ActionSkipSucceeded, backfill.ActionSkipFailed, backfill.ActionSkipActive} {
		if counts[action] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", strings.ReplaceAll(string(action), ":", ""), counts[action]))
		}
	}
	return strings.Join(parts, ", ")
}

func countRuns(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

func formatBackfillTime(t time.Time) string {
	return t.Format(backfillTimeLayout)
}

func reportBackfillSubmit(out io.Writer, format string, result backfill.SubmitResult) error {
	if format == backfillFormatJSON {
		payload := map[string]any{"plan": result.Plan}
		if result.Record != nil {
			payload["id"] = result.Record.ID
			payload["existing"] = result.Existing
		}
		return json.NewEncoder(out).Encode(payload)
	}
	switch {
	case result.Record == nil:
		_, err := fmt.Fprintln(out, "Nothing to run.")
		return err
	case result.Existing:
		_, err := fmt.Fprintf(out, "Backfill %s with the same options is already open.\n", result.Record.ID)
		return err
	default:
		_, err := fmt.Fprintf(out, "Started backfill %s: %s.\n", result.Record.ID, countRuns(len(result.Record.Slots)))
		return err
	}
}
