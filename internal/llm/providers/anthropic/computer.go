// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

const (
	computerToolsetType      = "computer_toolset_20260801"
	computerToolsetName      = "computer"
	defaultComputerMaxTokens = 16000
	stopReasonRefusal        = "refusal"
	// computerSkippedText is the exact result the toolset expects for a
	// batched action not run because an earlier one failed.
	computerSkippedText = "Not executed: an earlier computer action in this turn failed."
)

// Stop reasons that can cut a tool_use block short.
var truncatingStopReasons = map[string]bool{"max_tokens": true, "model_context_window_exceeded": true}

// computerImageLimit keeps screenshots within the per-image budget of 4784
// visual tokens and within 2000 pixels per side, the limit once a request
// carries more than 20 images. The conversation is append-only, so old
// screenshots are never removed.
var computerImageLimit = computeruse.ImageLimit{LongEdge: 2000, MaxPixels: 3_600_000}

// claudeModelPattern reads the family and version from a model ID such as
// claude-opus-4-8, anthropic.claude-sonnet-5 or claude-opus-4-5@20251101.
var claudeModelPattern = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:-(\d+))?`)

// legacyClaudePattern matches model IDs of the claude-3 generation.
var legacyClaudePattern = regexp.MustCompile(`claude-\d`)

// toolsetSupported reports whether a model takes the computer toolset.
// Earlier models only take the computer_20251124 tool and older versions.
// Unrecognized IDs, such as custom deployments, are assumed to support it.
func toolsetSupported(model string) bool {
	if legacyClaudePattern.MatchString(model) {
		return false
	}
	match := claudeModelPattern.FindStringSubmatch(model)
	if match == nil {
		return true
	}
	family := match[1]
	major, _ := strconv.Atoi(match[2])
	minor, _ := strconv.Atoi(match[3])
	switch {
	case family == "fable" || family == "mythos" || major >= 5:
		return true
	case family == "opus" && major == 4:
		// A date suffix such as 20250929 is not a minor version.
		return minor >= 8 && minor < 100
	default:
		return false
	}
}

const computerSystemPrompt = `You operate a computer with the computer tools. Coordinates are pixels in the screenshots you receive, measured from the top-left corner. Take a screenshot whenever you need to see the result of your actions.
`

func init() {
	computeruse.RegisterNative(llm.ProviderAnthropic, newComputerSession)
}

// computerSession drives Claude through the computer toolset. The
// conversation is append-only: assistant turns are sent back exactly as
// received, which keeps their thinking blocks valid.
type computerSession struct {
	provider *Provider
	opts     computeruse.Options
	messages []computerMessage
	// pending are the tool calls of the last turn, which the next
	// observation must answer in order.
	pending []computerCall
}

type computerMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// computerCall is a tool_use block awaiting its result. err is set when the
// block could not be turned into an action.
type computerCall struct {
	id      string
	toolset string
	err     error
}

func newComputerSession(provider llm.Provider, opts computeruse.Options) (computeruse.Session, error) {
	p, ok := llm.Unwrap(provider).(*Provider)
	if !ok {
		return nil, fmt.Errorf("%s: computer use needs an Anthropic provider, got %T", providerName, provider)
	}
	if !toolsetSupported(opts.Model) {
		return nil, fmt.Errorf("%s: %s: %w", providerName, opts.Model, computeruse.ErrModelNotSupported)
	}
	return &computerSession{provider: p, opts: opts}, nil
}

func (s *computerSession) ImageLimit() computeruse.ImageLimit {
	return computerImageLimit
}

func (s *computerSession) Next(ctx context.Context, obs computeruse.Observation) (*computeruse.Turn, error) {
	s.messages = append(s.messages, computerMessage{Role: "user", Content: s.observationContent(obs)})

	body, err := json.Marshal(s.request())
	if err != nil {
		return nil, err
	}
	respBody, err := s.provider.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = respBody.Close() }()

	var resp computerResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}
	if resp.StopReason == stopReasonRefusal {
		return nil, fmt.Errorf("%s: the model declined the request: %s", providerName, resp.StopDetails.describe())
	}
	if truncatingStopReasons[resp.StopReason] && endsWithToolUse(resp.Content) {
		return nil, fmt.Errorf("%s: the model's answer was cut off (%s) in the middle of an action; raise max_tokens", providerName, resp.StopReason)
	}
	s.messages = append(s.messages, computerMessage{Role: "assistant", Content: resp.Content})
	return s.turn(resp)
}

func (s *computerSession) request() map[string]any {
	maxTokens := defaultComputerMaxTokens
	if s.opts.MaxTokens != nil {
		maxTokens = *s.opts.MaxTokens
	}
	system := computerSystemPrompt + computeruse.DoneInstruction
	if s.opts.System != "" {
		system += "\n\n" + s.opts.System
	}
	return map[string]any{
		"model":      s.opts.Model,
		"max_tokens": maxTokens,
		"system":     system,
		"tools": []any{
			map[string]any{"type": computerToolsetType},
			map[string]any{
				"name":         computeruse.DoneToolName,
				"description":  computeruse.DoneToolDescription,
				"input_schema": computeruse.DoneParameters(),
			},
		},
		"tool_choice": map[string]any{"type": "auto"},
		"messages":    s.messages,
	}
}

// observationContent answers the pending tool calls. The screenshot is
// only sent on its own at the start and when no tool call asked for one.
func (s *computerSession) observationContent(obs computeruse.Observation) []any {
	results := make(map[string]computeruse.Result, len(obs.Results))
	for _, result := range obs.Results {
		results[result.CallID] = result
	}
	var content []any
	for _, call := range s.pending {
		block := map[string]any{"type": "tool_result", "tool_use_id": call.id}
		if call.toolset != "" {
			block["toolset_name"] = call.toolset
		}
		result, ok := results[call.id]
		switch {
		case call.err != nil:
			result = computeruse.Result{Error: call.err.Error()}
		case !ok && call.toolset != "":
			result = computeruse.Result{Skipped: true}
		}
		text := result.Text()
		if result.Skipped {
			text = computerSkippedText
		}
		if result.Image != nil && !result.Failed() {
			block["content"] = []any{imageBlock(*result.Image)}
		} else {
			block["content"] = []any{map[string]any{"type": "text", "text": text}}
		}
		if result.Failed() {
			block["is_error"] = true
		}
		content = append(content, block)
	}

	var text string
	if len(s.messages) == 0 {
		text = "Task: " + s.opts.Task + "\n\n"
	}
	if obs.Note != "" {
		text += obs.Note + "\n\n"
	}
	if len(s.pending) == 0 {
		text += fmt.Sprintf("Current screen (%dx%d pixels).", obs.Screen.Width, obs.Screen.Height)
		content = append(content, map[string]any{"type": "text", "text": text}, imageBlock(obs.Screen.Image))
	} else if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	return content
}

func (s *computerSession) turn(resp computerResponse) (*computeruse.Turn, error) {
	turn := &computeruse.Turn{Usage: llm.Usage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
	}}
	s.pending = s.pending[:0]
	// Actions after a call that cannot be performed are not run, as the
	// toolset's batch rules require; they are answered as skipped.
	halted := false
	for _, raw := range resp.Content {
		var block computerBlock
		if err := json.Unmarshal(raw, &block); err != nil {
			return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode content block: %w", err))
		}
		switch block.Type {
		case "text":
			turn.Text += block.Text
		case "tool_use":
			call := computerCall{id: block.ID, toolset: block.ToolsetName}
			switch {
			case block.ToolsetName == computerToolsetName && halted:
			case block.ToolsetName == computerToolsetName:
				var action computeruse.Action
				if action, call.err = computerAction(block); call.err == nil {
					turn.Actions = append(turn.Actions, action)
				} else {
					halted = true
				}
			case block.Name == computeruse.DoneToolName:
				turn.Done, call.err = computeruse.ParseDone(block.Input)
			default:
				call.err = fmt.Errorf("unknown tool %q", block.Name)
			}
			s.pending = append(s.pending, call)
		}
	}
	return turn, nil
}

// endsWithToolUse reports whether the last content block is a tool_use.
func endsWithToolUse(content []json.RawMessage) bool {
	if len(content) == 0 {
		return false
	}
	var block struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(content[len(content)-1], &block) == nil && block.Type == "tool_use"
}

type computerResponse struct {
	Content     []json.RawMessage `json:"content"`
	StopReason  string            `json:"stop_reason"`
	StopDetails *stopDetails      `json:"stop_details"`
	Usage       struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type stopDetails struct {
	Category    string `json:"category"`
	Explanation string `json:"explanation"`
}

func (d *stopDetails) describe() string {
	switch {
	case d == nil:
		return "no reason given"
	case d.Explanation != "":
		return d.Explanation
	case d.Category != "":
		return d.Category
	default:
		return "no reason given"
	}
}

type computerBlock struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	ToolsetName string          `json:"toolset_name"`
	Input       json.RawMessage `json:"input"`
}

// computerInput holds the input fields of every computer toolset member.
type computerInput struct {
	Coordinate      []int   `json:"coordinate"`
	StartCoordinate []int   `json:"start_coordinate"`
	Region          []int   `json:"region"`
	Text            string  `json:"text"`
	ScrollDirection string  `json:"scroll_direction"`
	ScrollAmount    int     `json:"scroll_amount"`
	Repeat          int     `json:"repeat"`
	Duration        float64 `json:"duration"`
}

var errMissingCoordinate = errors.New("coordinate is required")

// computerAction converts a computer toolset member call into an action.
func computerAction(block computerBlock) (computeruse.Action, error) {
	var input computerInput
	if len(block.Input) > 0 {
		if err := json.Unmarshal(block.Input, &input); err != nil {
			return computeruse.Action{}, fmt.Errorf("invalid input: %w", err)
		}
	}
	point, err := optionalPoint(input.Coordinate)
	if err != nil {
		return computeruse.Action{}, err
	}
	action := computeruse.Action{CallID: block.ID, Point: point}
	switch block.Name {
	case "screenshot":
		action.Kind = computeruse.KindScreenshot
	case "zoom":
		if len(input.Region) != 4 {
			return computeruse.Action{}, errors.New("region must be [x0, y0, x1, y1]")
		}
		action.Kind = computeruse.KindZoom
		action.Region = &computeruse.Rect{
			Min: computeruse.Point{X: input.Region[0], Y: input.Region[1]},
			Max: computeruse.Point{X: input.Region[2], Y: input.Region[3]},
		}
	case "left_click", "right_click", "middle_click", "double_click", "triple_click":
		action.Kind = computeruse.KindClick
		action.Button, action.Count = clickButton(block.Name)
		action.Modifiers = computeruse.SplitKeys(input.Text)
	case "left_click_drag":
		start, err := optionalPoint(input.StartCoordinate)
		if err != nil {
			return computeruse.Action{}, err
		}
		if start == nil || point == nil {
			return computeruse.Action{}, errors.New("start_coordinate and coordinate are required")
		}
		action.Kind = computeruse.KindDrag
		action.Path = []computeruse.Point{*start, *point}
		action.Point = nil
		action.Modifiers = computeruse.SplitKeys(input.Text)
	case "mouse_move":
		if point == nil {
			return computeruse.Action{}, errMissingCoordinate
		}
		action.Kind = computeruse.KindMove
	case "left_mouse_down":
		action.Kind = computeruse.KindMouseDown
		action.Button = computeruse.ButtonLeft
	case "left_mouse_up":
		action.Kind = computeruse.KindMouseUp
		action.Button = computeruse.ButtonLeft
	case "cursor_position":
		action.Kind = computeruse.KindCursorPosition
	case "scroll":
		action.Kind = computeruse.KindScroll
		action.Modifiers = computeruse.SplitKeys(input.Text)
		if action.ScrollX, action.ScrollY, err = computeruse.ScrollDelta(input.ScrollDirection, input.ScrollAmount); err != nil {
			return computeruse.Action{}, err
		}
	case "type":
		action.Kind = computeruse.KindType
		action.Text = input.Text
	case "key":
		action.Kind = computeruse.KindKey
		action.Keys = computeruse.SplitKeys(input.Text)
		action.Repeat = input.Repeat
	case "hold_key":
		action.Kind = computeruse.KindHoldKey
		action.Keys = computeruse.SplitKeys(input.Text)
		action.Duration = seconds(input.Duration)
	case "wait":
		action.Kind = computeruse.KindWait
		action.Duration = seconds(input.Duration)
	default:
		return computeruse.Action{}, fmt.Errorf("unsupported computer action %q", block.Name)
	}
	return action, nil
}

func clickButton(name string) (button string, count int) {
	switch name {
	case "right_click":
		return computeruse.ButtonRight, 1
	case "middle_click":
		return computeruse.ButtonMiddle, 1
	case "double_click":
		return computeruse.ButtonLeft, 2
	case "triple_click":
		return computeruse.ButtonLeft, 3
	default:
		return computeruse.ButtonLeft, 1
	}
}

func optionalPoint(coordinate []int) (*computeruse.Point, error) {
	switch len(coordinate) {
	case 0:
		return nil, nil
	case 2:
		return &computeruse.Point{X: coordinate[0], Y: coordinate[1]}, nil
	default:
		return nil, errors.New("coordinate must be [x, y]")
	}
}

func seconds(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}
