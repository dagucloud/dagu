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
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	defaultElementsLimit = 400
	// watchInterval spaces the looks under the pointer while watching.
	watchInterval = 50 * time.Millisecond
)

// Error codes of dagu computer elements, beside the problem codes of
// dagu computer check.
const (
	elementsCodeInvalidInput = "invalid_input"
	elementsCodeUnsupported  = "unsupported"
	elementsCodeLoadFailed   = "load_failed"
	elementsCodeNoElements   = "no_elements"
	elementsCodeNotFound     = "not_found"
	elementsCodeAmbiguous    = "ambiguous"
	elementsCodeFailed       = "failed"
)

var (
	elementsFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "text",
		usage:        "Output format: text or json (default: text)",
	}
	elementsLimitFlag = commandLineFlag{
		name:         "limit",
		defaultValue: strconv.Itoa(defaultElementsLimit),
		usage:        "Most elements to list (default: 400)",
	}
	elementsAtPointerFlag = commandLineFlag{
		name:   "at-pointer",
		isBool: true,
		usage:  "Report the element under the pointer",
	}
	elementsAfterFlag = commandLineFlag{
		name:         "after",
		defaultValue: "0s",
		usage:        "With --at-pointer, how long to wait first, such as 3s",
	}
	elementsWatchFlag = commandLineFlag{
		name:   "watch",
		isBool: true,
		usage:  "Report the element under the pointer whenever it changes, one JSON object per line, until stdin closes or the command is interrupted",
	}
	elementsMatchFlag = commandLineFlag{
		name:  "match",
		usage: "Report the elements a selector matches; the selector is JSON, or - to read it from stdin",
	}
)

func computerElementsCommand() *cobra.Command {
	return NewCommand(&cobra.Command{
		Use:   "elements",
		Short: "List the accessible elements of the window in front",
		Long: `List the accessible elements of the window in front: each with its role,
name, id, value, the label it is linked to, its bounds in display pixels,
and its path from the window down.

With --at-pointer, report the element under the pointer, after --after.
With --watch, report the element under the pointer whenever it changes, as
one JSON object per line, until stdin closes or the command is
interrupted. With --match, report the elements a selector matches: the
command succeeds only when exactly one does.

A selector is JSON with role and at least one of name, id, or near, such
as {"role": "button", "name": "保存", "in": {"role": "group", "name":
"支払情報"}}. Names take * for any run of characters.

With --format json, the result is one object. A failing command prints
{"error": {"code", "message"}} and exits 1. Codes are invalid_input,
unsupported, load_failed, no_elements, not_found, ambiguous, failed, and
the problem codes of dagu computer check.

Elements are read on 64-bit Windows.
`,
		Args: cobra.NoArgs,
	}, []commandLineFlag{elementsFormatFlag, elementsLimitFlag, elementsAtPointerFlag, elementsAfterFlag, elementsWatchFlag, elementsMatchFlag}, runComputerElements)
}

// elementsError is the error object a failed command prints.
type elementsError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *elementsError) Error() string { return e.Message }

func invalidElementsInput(format string, args ...any) *elementsError {
	return &elementsError{Code: elementsCodeInvalidInput, Message: fmt.Sprintf(format, args...)}
}

// elementsOptions are the command's parsed flags.
type elementsOptions struct {
	format    string
	limit     int
	atPointer bool
	after     time.Duration
	watch     bool
	match     *desktop.Selector
}

// elementsOutline is the JSON result of listing a window.
type elementsOutline struct {
	Window    desktop.Element   `json:"window"`
	Elements  []desktop.Element `json:"elements"`
	Count     int               `json:"count"`
	Truncated bool              `json:"truncated"`
}

// elementsMatches is the JSON result of --match. The error is set when
// the selector did not match exactly one element, with the matches still
// listed so a person can see what to narrow.
type elementsMatches struct {
	Error   *elementsError    `json:"error,omitempty"`
	Window  desktop.Element   `json:"window"`
	Count   int               `json:"count"`
	Matches []desktop.Element `json:"matches"`
}

func runComputerElements(ctx *Context, _ []string) error {
	opts, err := parseElementsOptions(ctx)
	if err != nil {
		return writeElementsError(ctx, opts.format, err)
	}
	if diag := desktop.Check(); len(diag.Problems) > 0 {
		return writeElementsError(ctx, opts.format, &elementsError{Code: diag.Problems[0].Code, Message: diag.Err().Error()})
	}
	els, err := desktop.OpenElements()
	if err != nil {
		code := elementsCodeLoadFailed
		if errors.Is(err, desktop.ErrElementsUnsupported) {
			code = elementsCodeUnsupported
		}
		return writeElementsError(ctx, opts.format, &elementsError{Code: code, Message: err.Error()})
	}
	defer func() { _ = els.Close() }()
	switch {
	case opts.watch:
		return watchElements(ctx, opts, els)
	case opts.atPointer:
		return elementAtPointer(ctx, opts, els)
	case opts.match != nil:
		return matchElements(ctx, opts, els)
	default:
		return outlineElements(ctx, opts, els)
	}
}

// parseElementsOptions reads the flags and rejects what cannot run, before
// the desktop is touched. The format is settled first, so a later error
// is printed the way the person asked.
func parseElementsOptions(ctx *Context) (elementsOptions, error) {
	opts := elementsOptions{format: "text"}
	format, err := ctx.StringParam("format")
	if err != nil {
		return opts, err
	}
	if format != "text" && format != "json" {
		return opts, invalidElementsInput("--format %q: use text or json", format)
	}
	opts.format = format
	limit, err := ctx.StringParam("limit")
	if err != nil {
		return opts, err
	}
	if opts.limit, err = strconv.Atoi(limit); err != nil || opts.limit <= 0 {
		return opts, invalidElementsInput("--limit %q: use a positive number", limit)
	}
	if opts.atPointer, err = ctx.Command.Flags().GetBool("at-pointer"); err != nil {
		return opts, err
	}
	if opts.watch, err = ctx.Command.Flags().GetBool("watch"); err != nil {
		return opts, err
	}
	match, err := ctx.StringParam("match")
	if err != nil {
		return opts, err
	}
	modes := 0
	for _, on := range []bool{opts.atPointer, opts.watch, match != ""} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		return opts, invalidElementsInput("use one of --at-pointer, --watch, or --match")
	}
	after, err := ctx.StringParam("after")
	if err != nil {
		return opts, err
	}
	if opts.after, err = time.ParseDuration(after); err != nil || opts.after < 0 {
		return opts, invalidElementsInput("--after %q: use a duration such as 3s", after)
	}
	if opts.after > 0 && !opts.atPointer {
		return opts, invalidElementsInput("--after needs --at-pointer")
	}
	if match != "" {
		data := []byte(match)
		if match == "-" {
			if data, err = readSelectorInput(ctx.Command.InOrStdin()); err != nil {
				return opts, err
			}
		}
		sel, err := desktop.ParseSelector(data)
		if err != nil {
			return opts, invalidElementsInput("--match: %v", err)
		}
		opts.match = &sel
	}
	return opts, nil
}

// readSelectorInput reads a selector from stdin, which must not be a
// terminal.
func readSelectorInput(in io.Reader) ([]byte, error) {
	if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return nil, invalidElementsInput(`give the selector on stdin, such as: echo '{"role": "button", "name": "保存"}' | dagu computer elements --match -`)
	}
	data, err := io.ReadAll(io.LimitReader(in, maxSessionInputSize+1))
	if err != nil {
		return nil, fmt.Errorf("read the selector: %w", err)
	}
	if len(data) > maxSessionInputSize {
		return nil, invalidElementsInput("the selector is larger than %d bytes", maxSessionInputSize)
	}
	return data, nil
}

// writeElementsError prints a failed command's error, as JSON when asked,
// and returns it so the command exits 1.
func writeElementsError(ctx *Context, format string, err error) error {
	if format != "json" {
		return err
	}
	var elementsErr *elementsError
	if !errors.As(err, &elementsErr) {
		elementsErr = &elementsError{Code: elementsCodeFailed, Message: err.Error()}
	}
	if writeErr := writeIndentedJSON(ctx.Command.OutOrStdout(), map[string]any{"error": elementsErr}); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}

// readError maps an error from reading elements onto a code.
func readError(err error) *elementsError {
	if elementsErr, ok := errors.AsType[*elementsError](err); ok {
		return elementsErr
	}
	code := elementsCodeFailed
	switch {
	case errors.Is(err, desktop.ErrNoElements):
		code = elementsCodeNoElements
	case errors.Is(err, desktop.ErrAmbiguous):
		code = elementsCodeAmbiguous
	case errors.Is(err, desktop.ErrNotFound):
		code = elementsCodeNotFound
	}
	return &elementsError{Code: code, Message: err.Error()}
}

func outlineElements(ctx *Context, opts elementsOptions, els desktop.Elements) error {
	window, err := els.FrontWindow()
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	elements, err := els.Outline(window, opts.limit+1)
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	// One element past the limit says the outline was cut, not how much.
	result := elementsOutline{Window: window, Elements: elements}
	if len(elements) > opts.limit {
		result.Elements, result.Truncated = elements[:opts.limit], true
	}
	if result.Elements == nil {
		result.Elements = []desktop.Element{}
	}
	result.Count = len(result.Elements)
	out := ctx.Command.OutOrStdout()
	if opts.format == "json" {
		return writeIndentedJSON(out, result)
	}
	writer := &lineWriter{out: out}
	writer.printf("Window: %s\n", describeWindow(window))
	for i, e := range result.Elements {
		writer.printf("%s[%d] %s\n", strings.Repeat("  ", max(len(e.Path)-1, 0)), i+1, describeElement(e))
	}
	if result.Truncated {
		writer.printf("… more elements; raise --limit to list them\n")
	}
	return writer.err
}

func elementAtPointer(ctx *Context, opts elementsOptions, els desktop.Elements) error {
	if opts.after > 0 {
		select {
		case <-time.After(opts.after):
		case <-ctx.Done():
			return writeElementsError(ctx, opts.format, ctx.Err())
		}
	}
	driver, err := desktop.Open()
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	defer func() { _ = driver.Close() }()
	at, err := driver.CursorPosition()
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	found, err := els.At(at.X, at.Y)
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	out := ctx.Command.OutOrStdout()
	if opts.format == "json" {
		return writeIndentedJSON(out, found)
	}
	_, err = fmt.Fprintf(out, "%s in %q\n", describeElement(found), found.Window)
	return err
}

// watchElements reports the element under the pointer whenever it
// changes, until stdin closes or the command is interrupted.
func watchElements(ctx *Context, opts elementsOptions, els desktop.Elements) error {
	driver, err := desktop.Open()
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	defer func() { _ = driver.Close() }()
	watchCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	watchCtx, cancel := context.WithCancel(watchCtx)
	defer cancel()
	go func() {
		_, _ = io.Copy(io.Discard, ctx.Command.InOrStdin())
		cancel()
	}()
	out := ctx.Command.OutOrStdout()
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-watchCtx.Done():
			return nil
		case <-ticker.C:
		}
		at, err := driver.CursorPosition()
		if err != nil {
			continue
		}
		found, err := els.At(at.X, at.Y)
		if err != nil {
			continue
		}
		key := fmt.Sprint(found.Window, found.ID, found.Path, found.Bounds)
		if key == last {
			continue
		}
		last = key
		if opts.format == "json" {
			err = encoder.Encode(found)
		} else {
			_, err = fmt.Fprintf(out, "%s in %q\n", describeElement(found), found.Window)
		}
		if err != nil {
			return err
		}
	}
}

func matchElements(ctx *Context, opts elementsOptions, els desktop.Elements) error {
	window, err := els.FrontWindow()
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	matches, err := els.Find(*opts.match)
	if err != nil {
		return writeElementsError(ctx, opts.format, readError(err))
	}
	result := elementsMatches{Window: window, Count: len(matches), Matches: matches}
	if result.Matches == nil {
		result.Matches = []desktop.Element{}
	}
	if _, err := desktop.One(matches, *opts.match); err != nil {
		result.Error = readError(err)
	}
	out := ctx.Command.OutOrStdout()
	if opts.format == "json" {
		if err := writeIndentedJSON(out, result); err != nil {
			return err
		}
	} else {
		writer := &lineWriter{out: out}
		writer.printf("Window: %s\n", describeWindow(window))
		writer.printf("Matches: %d\n", result.Count)
		for i, e := range result.Matches {
			writer.printf("[%d] %s\n", i+1, describeElement(e))
		}
		if writer.err != nil {
			return writer.err
		}
	}
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func describeWindow(w desktop.Element) string {
	text := fmt.Sprintf("%q", w.Name)
	if w.App != "" {
		text += fmt.Sprintf(" (%s)", w.App)
	}
	return fmt.Sprintf("%s at %d,%d %dx%d", text, w.Bounds.Min.X, w.Bounds.Min.Y, w.Bounds.Dx(), w.Bounds.Dy())
}

func describeElement(e desktop.Element) string {
	parts := []string{e.Role}
	if e.Name != "" {
		parts = append(parts, fmt.Sprintf("%q", e.Name))
	}
	if e.ID != "" {
		parts = append(parts, "id="+e.ID)
	}
	if e.Label != "" {
		parts = append(parts, fmt.Sprintf("label=%q", e.Label))
	}
	if e.Value != "" {
		parts = append(parts, fmt.Sprintf("value=%q", e.Value))
	}
	parts = append(parts, fmt.Sprintf("at %d,%d %dx%d", e.Bounds.Min.X, e.Bounds.Min.Y, e.Bounds.Dx(), e.Bounds.Dy()))
	return strings.Join(parts, " ")
}
