// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

const (
	responsesEndpoint   = "/responses"
	computerToolType    = "computer"
	defaultWaitDuration = 2 * time.Second
	responseFailed      = "failed"
	responseIncomplete  = "incomplete"
)

// computerImageLimit keeps screenshots at a display size computer-use
// models handle well.
var computerImageLimit = computeruse.ImageLimit{LongEdge: 1920, MaxPixels: 1920 * 1200}

const computerInstructions = `You operate a computer with the computer tool. Coordinates are pixels in the screenshots you receive, measured from the top-left corner.
`

func init() {
	computeruse.RegisterNative(llm.ProviderOpenAI, newComputerSession)
}

// gptVersionPattern reads the version from a model ID such as gpt-5.4-mini
// or ft:gpt-5.6-sol:org::id.
var gptVersionPattern = regexp.MustCompile(`gpt-(\d+)(?:\.(\d+))?`)

// reasoningModelPattern matches o-series model IDs such as o3 or o4-mini.
var reasoningModelPattern = regexp.MustCompile(`^o\d`)

// computerToolSupported reports whether a model takes the computer tool,
// which OpenAI offers from GPT-5.4 on, except for nano models. The older
// computer-use-preview model takes only the preview tool. Unrecognized IDs,
// such as custom deployments, are assumed to support it.
func computerToolSupported(model string) bool {
	switch {
	case model == "computer-use-preview", reasoningModelPattern.MatchString(model):
		return false
	case strings.Contains(model, "nano"):
		return false
	}
	match := gptVersionPattern.FindStringSubmatch(model)
	if match == nil {
		return true
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return major > 5 || (major == 5 && minor >= 4)
}

// computerSession drives a model through the Responses API computer tool.
// Each request continues the previous response on the server.
type computerSession struct {
	provider      *Provider
	opts          computeruse.Options
	previousID    string
	started       bool
	computerCalls []computerCall
	functionCalls []functionCall
}

// computerCall is a computer_call awaiting its output. checks are the
// safety checks it raised, which the output acknowledges once approved.
type computerCall struct {
	callID string
	err    error
	checks []safetyCheck
	// skipped marks a call not performed because an earlier action could
	// not be.
	skipped bool
}

type functionCall struct {
	callID string
	err    error
}

type safetyCheck struct {
	ID      string `json:"id"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func newComputerSession(provider llm.Provider, opts computeruse.Options) (computeruse.Session, error) {
	p, ok := llm.Unwrap(provider).(*Provider)
	if !ok {
		return nil, fmt.Errorf("%s: computer use needs an OpenAI provider, got %T", providerName, provider)
	}
	if !computerToolSupported(opts.Model) {
		return nil, fmt.Errorf("%s: %s: %w", providerName, opts.Model, computeruse.ErrModelNotSupported)
	}
	return &computerSession{provider: p, opts: opts}, nil
}

func (s *computerSession) ImageLimit() computeruse.ImageLimit {
	return computerImageLimit
}

func (s *computerSession) Next(ctx context.Context, obs computeruse.Observation) (*computeruse.Turn, error) {
	body, err := json.Marshal(s.request(obs))
	if err != nil {
		return nil, err
	}
	respBody, err := s.provider.httpClient.Do(ctx, s.provider.config.BaseURL+responsesEndpoint, body, s.provider.authHeaders())
	if err != nil {
		return nil, err
	}
	defer func() { _ = respBody.Close() }()

	var resp responsesResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}
	switch resp.Status {
	case responseFailed:
		message := "the response failed"
		if resp.Error != nil && resp.Error.Message != "" {
			message = resp.Error.Message
		}
		return nil, llm.WrapError(providerName, errors.New(message))
	case responseIncomplete:
		reason := "no reason given"
		if resp.IncompleteDetails != nil && resp.IncompleteDetails.Reason != "" {
			reason = resp.IncompleteDetails.Reason
		}
		return nil, fmt.Errorf("%s: the response is incomplete (%s); raise max_tokens if the model ran out of output tokens", providerName, reason)
	}
	s.previousID = resp.ID
	return s.turn(resp)
}

func (s *computerSession) request(obs computeruse.Observation) map[string]any {
	instructions := computerInstructions + computeruse.DoneInstruction
	if s.opts.System != "" {
		instructions += "\n\n" + s.opts.System
	}
	request := map[string]any{
		"model":        s.opts.Model,
		"instructions": instructions,
		"tools": []any{
			map[string]any{"type": computerToolType},
			map[string]any{
				"type":        "function",
				"name":        computeruse.DoneToolName,
				"description": computeruse.DoneToolDescription,
				"parameters":  computeruse.DoneParameters(),
			},
		},
		"truncation": "auto",
		"input":      s.input(obs),
	}
	if s.previousID != "" {
		request["previous_response_id"] = s.previousID
	}
	if s.opts.MaxTokens != nil {
		request["max_output_tokens"] = *s.opts.MaxTokens
	}
	return request
}

// input answers the previous response's calls. Every computer call is
// answered with the current screen; failed actions are described in a
// following user message because a computer call output carries no text.
func (s *computerSession) input(obs computeruse.Observation) []any {
	screenshot := map[string]any{
		"type":      "computer_screenshot",
		"image_url": obs.Screen.Image.DataURL(),
		"detail":    "original",
	}
	var input []any
	for _, call := range s.computerCalls {
		output := map[string]any{
			"type":    "computer_call_output",
			"call_id": call.callID,
			"output":  screenshot,
		}
		if obs.Acknowledged && len(call.checks) > 0 {
			output["acknowledged_safety_checks"] = call.checks
		}
		input = append(input, output)
	}
	for _, call := range s.functionCalls {
		output := "OK"
		if call.err != nil {
			output = "Error: " + call.err.Error()
		}
		input = append(input, map[string]any{"type": "function_call_output", "call_id": call.callID, "output": output})
	}

	var text strings.Builder
	if !s.started {
		text.WriteString("Task: " + s.opts.Task + "\n\n")
	}
	for _, call := range s.computerCalls {
		switch {
		case call.err != nil:
			text.WriteString("The computer call could not be performed: " + call.err.Error() + "\n")
		case call.skipped:
			text.WriteString("The computer call was not performed: an earlier action failed.\n")
		}
	}
	for i, result := range obs.Results {
		switch {
		case result.Error != "":
			fmt.Fprintf(&text, "Action %d failed: %s\n", i+1, result.Error)
		case result.Skipped:
			fmt.Fprintf(&text, "Action %d was not performed: an earlier action failed.\n", i+1)
		}
	}
	if obs.Note != "" {
		text.WriteString(obs.Note + "\n")
	}

	var content []any
	if len(s.computerCalls) == 0 {
		fmt.Fprintf(&text, "Current screen (%dx%d pixels).", obs.Screen.Width, obs.Screen.Height)
		content = append(content,
			map[string]any{"type": "input_text", "text": text.String()},
			map[string]any{"type": "input_image", "image_url": obs.Screen.Image.DataURL(), "detail": "original"},
		)
	} else if text.Len() > 0 {
		content = append(content, map[string]any{"type": "input_text", "text": text.String()})
	}
	if len(content) > 0 {
		input = append(input, map[string]any{"role": "user", "content": content})
	}
	s.started = true
	return input
}

func (s *computerSession) turn(resp responsesResponse) (*computeruse.Turn, error) {
	turn := &computeruse.Turn{Usage: llm.Usage{
		PromptTokens:     resp.Usage.InputTokens,
		CompletionTokens: resp.Usage.OutputTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}}
	s.computerCalls = s.computerCalls[:0]
	s.functionCalls = s.functionCalls[:0]

	var confirmations []string
	// Actions after one that cannot be performed are not run, as when an
	// action fails.
	halted := false
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					turn.Text += part.Text
				case "refusal":
					return nil, fmt.Errorf("%s: the model declined the request: %s", providerName, part.Refusal)
				}
			}
		case "computer_call":
			call := computerCall{callID: item.CallID}
			actions := item.Actions
			if len(actions) == 0 && item.Action != nil {
				actions = []responsesAction{*item.Action}
			}
			call.skipped = halted
			for _, raw := range actions {
				if halted {
					break
				}
				action, err := computerAction(item.CallID, raw)
				if err != nil {
					call.err = err
					halted = true
					break
				}
				turn.Actions = append(turn.Actions, action)
			}
			call.checks = item.PendingSafetyChecks
			for _, check := range item.PendingSafetyChecks {
				confirmations = append(confirmations, check.describe())
			}
			s.computerCalls = append(s.computerCalls, call)
		case "function_call":
			call := functionCall{callID: item.CallID}
			if item.Name == computeruse.DoneToolName {
				turn.Done, call.err = computeruse.ParseDone([]byte(item.Arguments))
			} else {
				call.err = fmt.Errorf("unknown tool %q", item.Name)
			}
			s.functionCalls = append(s.functionCalls, call)
		}
	}
	turn.Confirmation = strings.Join(confirmations, "\n")
	return turn, nil
}

// describe names a safety check for the person asked to confirm it. The
// message may be absent, so the code or ID stands in.
func (c safetyCheck) describe() string {
	switch {
	case c.Message != "":
		return c.Message
	case c.Code != "":
		return c.Code
	default:
		return "safety check " + c.ID
	}
}

type responsesResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []responsesItem `json:"output"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type responsesItem struct {
	Type                string            `json:"type"`
	CallID              string            `json:"call_id"`
	Name                string            `json:"name"`
	Arguments           string            `json:"arguments"`
	Action              *responsesAction  `json:"action"`
	Actions             []responsesAction `json:"actions"`
	PendingSafetyChecks []safetyCheck     `json:"pending_safety_checks"`
	Content             []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
}

type responsesAction struct {
	Type    string   `json:"type"`
	Button  string   `json:"button"`
	X       *int     `json:"x"`
	Y       *int     `json:"y"`
	Path    []xy     `json:"path"`
	Keys    []string `json:"keys"`
	ScrollX int      `json:"scroll_x"`
	ScrollY int      `json:"scroll_y"`
	Text    string   `json:"text"`
}

type xy struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// computerAction converts a Responses API computer action. Modifier keys
// on pointer actions arrive in keys.
func computerAction(callID string, raw responsesAction) (computeruse.Action, error) {
	action := computeruse.Action{CallID: callID}
	var point *computeruse.Point
	if raw.X != nil && raw.Y != nil {
		point = &computeruse.Point{X: *raw.X, Y: *raw.Y}
	}
	switch raw.Type {
	case "click":
		button, err := clickButton(raw.Button)
		if err != nil {
			return computeruse.Action{}, err
		}
		action.Kind = computeruse.KindClick
		action.Point = point
		action.Button = button
		action.Count = 1
		action.Modifiers = raw.Keys
	case "double_click":
		action.Kind = computeruse.KindClick
		action.Point = point
		action.Button = computeruse.ButtonLeft
		action.Count = 2
		action.Modifiers = raw.Keys
	case "drag":
		if len(raw.Path) < 2 {
			return computeruse.Action{}, errors.New("drag needs at least two points")
		}
		action.Kind = computeruse.KindDrag
		for _, p := range raw.Path {
			action.Path = append(action.Path, computeruse.Point{X: p.X, Y: p.Y})
		}
		action.Modifiers = raw.Keys
	case "move":
		if point == nil {
			return computeruse.Action{}, errors.New("move needs x and y")
		}
		action.Kind = computeruse.KindMove
		action.Point = point
		action.Modifiers = raw.Keys
	case "scroll":
		action.Kind = computeruse.KindScroll
		action.Point = point
		action.ScrollX = computeruse.PixelsToNotches(raw.ScrollX)
		action.ScrollY = computeruse.PixelsToNotches(raw.ScrollY)
		action.Modifiers = raw.Keys
	case "keypress":
		if len(raw.Keys) == 0 {
			return computeruse.Action{}, errors.New("keypress needs keys")
		}
		action.Kind = computeruse.KindKey
		action.Keys = raw.Keys
	case "type":
		action.Kind = computeruse.KindType
		action.Text = raw.Text
	case "wait":
		action.Kind = computeruse.KindWait
		action.Duration = defaultWaitDuration
	case "screenshot":
		action.Kind = computeruse.KindScreenshot
	default:
		return computeruse.Action{}, fmt.Errorf("unsupported computer action %q", raw.Type)
	}
	return action, nil
}

func clickButton(button string) (string, error) {
	switch button {
	case "", "left":
		return computeruse.ButtonLeft, nil
	case "right":
		return computeruse.ButtonRight, nil
	case "wheel", "middle":
		return computeruse.ButtonMiddle, nil
	default:
		return "", fmt.Errorf("unsupported mouse button %q", button)
	}
}
