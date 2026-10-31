// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/spf13/cobra"
)

const (
	// defaultCalendarFireCount is how many fires `calendar fires` lists.
	defaultCalendarFireCount = 10
	calendarFireTimeLayout   = "Mon 2006-01-02 15:04 -07:00"
	calendarDateLayout       = "Mon 2006-01-02"
)

var (
	calendarFromFlag = commandLineFlag{
		name:  "from",
		usage: "List fires after this RFC 3339 time instead of now",
	}
	calendarCountFlag = commandLineFlag{
		name:         "count",
		usage:        "Number of fires to list",
		defaultValue: strconv.Itoa(defaultCalendarFireCount),
	}
)

// Calendar creates the 'calendar' command group for business calendars.
func Calendar() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "calendar",
		Short: "Check business calendars and preview the runs they allow",
		Long: `Business calendars list the days a schedule may fire on. A schedule entry
names one with calendar, or keeps only some business days of each month with
business_days. Calendar files live in paths.calendars_dir.`,
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(calendarCheckCommand())
	cmd.AddCommand(calendarFiresCommand())
	return cmd
}

func calendarCheckCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "check [calendar files...]",
		Short: "Validate calendar files",
		Long: `Validate calendar files, by default every file in paths.calendars_dir.
A calendar given as a path is checked with the calendars beside it, which
its include list names. Exits non-zero when any calendar is invalid.

To check that the DAGs naming a calendar find it, run dagu validate on them.`,
	}, nil, func(ctx *Context, args []string) error {
		return runCalendarCheck(ctx.Command.OutOrStdout(), ctx.Config.Paths.CalendarsDir, args)
	})
}

func runCalendarCheck(out io.Writer, calendarsDir string, files []string) error {
	type target struct{ name, path string }
	var targets []target
	if len(files) == 0 {
		summaries, err := spec.ListCalendars(calendarsDir)
		if err != nil {
			return err
		}
		for _, summary := range summaries {
			if !summary.Builtin {
				targets = append(targets, target{
					name: summary.Name,
					path: filepath.Join(calendarsDir, summary.Name+spec.CalendarFileExtension),
				})
			}
		}
	}
	for _, file := range files {
		targets = append(targets, target{
			name: strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)),
			path: file,
		})
	}
	if len(targets) == 0 {
		_, err := fmt.Fprintf(out, "No calendar files in %s\n", calendarsDir)
		return err
	}

	failed := 0
	for _, target := range targets {
		cal, problems := checkCalendarFile(target.name, target.path)
		if len(problems) > 0 {
			failed++
			for _, problem := range problems {
				if _, err := fmt.Fprintf(out, "FAIL %s: %s\n", target.name, problem); err != nil {
					return err
				}
			}
			continue
		}
		through := ""
		if !cal.Through.IsZero() {
			through = ", through " + cal.Through.String()
		}
		if _, err := fmt.Fprintf(out, "ok   %s: %d holidays%s\n", cal.Name, len(cal.Holidays), through); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d calendars are invalid", failed, len(targets))
	}
	return nil
}

// checkCalendarFile validates calendar name, read from path, with the
// calendars beside it, and lists its problems with their line and column.
// A file named after a built-in calendar fails, as no schedule can reach it.
func checkCalendarFile(name, path string) (*ir.Calendar, []string) {
	data, err := os.ReadFile(path) //nolint:gosec // check reads the CLI-provided calendar file path.
	if err != nil {
		return nil, []string{err.Error()}
	}
	cal, errs := spec.NewCalendarSource(filepath.Dir(path)).Check(name, data)
	return cal, spec.ErrorMessages(data, errs)
}

func calendarFiresCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "fires [flags] <DAG definition>",
		Short: "List the next times a DAG's start schedules fire",
		Long: `List the next times a DAG's start schedules fire, with the holidays each
skips and the business day it matches.

Examples:
  dagu calendar fires payroll
  dagu calendar fires payroll.yaml --from 2027-01-01T00:00:00+09:00 --count 20`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{calendarFromFlag, calendarCountFlag}, runCalendarFires)
}

func runCalendarFires(ctx *Context, args []string) error {
	from := time.Now().In(ctx.Config.Core.Location)
	if value, _ := ctx.Command.Flags().GetString("from"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return fmt.Errorf("--from must be an RFC 3339 time: %w", err)
		}
		// --from moves the start, not the zone of schedules that name none.
		from = parsed.In(ctx.Config.Core.Location)
	}
	countValue, _ := ctx.Command.Flags().GetString("count")
	count, err := strconv.Atoi(countValue)
	if err != nil || count <= 0 {
		return fmt.Errorf("--count must be a positive integer")
	}

	loadOpts := []spec.LoadOption{
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.WithDAGsDir(ctx.Config.Paths.DAGsDir),
		spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(ctx.Config.Paths.DAGsDir)),
		spec.WithCalendarsDir(ctx.Config.Paths.CalendarsDir),
	}
	if ctx.Config.Paths.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(ctx.Config.Paths.BaseConfig))
	}
	dag, err := spec.Load(ctx, args[0], loadOpts...)
	if err != nil {
		return err
	}
	return renderCalendarFires(ctx.Command.OutOrStdout(), dag, dag.UpcomingFires(from, count))
}

func renderCalendarFires(out io.Writer, dag *ir.DAG, fires []ir.ScheduleFire) error {
	if len(fires) == 0 {
		_, err := fmt.Fprintf(out, "%s has no upcoming runs\n", dag.Name)
		return err
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "TIME\tSCHEDULE\tNOTE"); err != nil {
		return err
	}
	for _, fire := range fires {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n",
			fire.Time.Format(calendarFireTimeLayout),
			dag.Schedule[fire.ScheduleIndex].DisplayValue(),
			calendarFireNote(fire)); err != nil {
			return err
		}
	}
	return w.Flush()
}

// calendarFireNote explains why a fire falls where it does.
func calendarFireNote(fire ir.ScheduleFire) string {
	var notes []string
	if fire.BusinessDay != 0 {
		notes = append(notes, businessDayLabel(fire.BusinessDay))
	}
	if fire.ExtraBusinessDay {
		notes = append(notes, "extra business day")
	}
	for _, holiday := range fire.SkippedHolidays {
		day := time.Date(holiday.Date.Year, holiday.Date.Month, holiday.Date.Day, 0, 0, 0, 0, time.UTC)
		note := "skips " + day.Format(calendarDateLayout)
		if holiday.Name != "" {
			note += " (" + holiday.Name + ")"
		}
		notes = append(notes, note)
	}
	return strings.Join(notes, "; ")
}

// businessDayLabel names a business-day position: "3rd business day" or
// "2nd to last business day".
func businessDayLabel(position int) string {
	switch {
	case position == -1:
		return "last business day"
	case position < 0:
		return ordinal(-position) + " to last business day"
	default:
		return ordinal(position) + " business day"
	}
}

func ordinal(n int) string {
	suffix := "th"
	switch n % 10 {
	case 1:
		suffix = "st"
	case 2:
		suffix = "nd"
	case 3:
		suffix = "rd"
	}
	if n%100 >= 11 && n%100 <= 13 {
		suffix = "th"
	}
	return strconv.Itoa(n) + suffix
}
