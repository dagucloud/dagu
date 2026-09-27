// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
)

const (
	desktopEnvironment = "ENVIRONMENT_DESKTOP"
	// normalizedScale is the size of the coordinate grid Gemini computer use
	// reports positions on, regardless of the screenshot size.
	normalizedScale      = 1000
	requireConfirmation  = "require_confirmation"
	defaultWaitDuration  = 5 * time.Second
	defaultScrollNotches = 3
)

// computerImageLimit is the screen size Gemini computer use is tuned for.
var computerImageLimit = computeruse.ImageLimit{LongEdge: 1440, MaxPixels: 1440 * 900}

// browserOnlyFunctions have no meaning on a desktop.
var browserOnlyFunctions = []string{"navigate", "go_back", "go_forward", "search", "open_web_browser"}

const computerInstructions = `You operate a computer with the computer use tool.
`

func init() {
	computeruse.RegisterNative(llm.ProviderGemini, newComputerSession)
}

// computerSession drives Gemini through its computer use tool. Model turns
// are sent back exactly as received, which keeps their thought signatures.
type computerSession struct {
	provider *Provider
	opts     computeruse.Options
	contents []any
	// pending are the function calls of the last turn, in order.
	pending []geminiCall
}

// geminiCall is a function call awaiting its response. id is Gemini's own
// call ID, which may be empty; key matches the call's action results.
type geminiCall struct {
	id      string
	key     string
	name    string
	confirm bool
	err     error
}

func newComputerSession(provider llm.Provider, opts computeruse.Options) (computeruse.Session, error) {
	p, ok := llm.Unwrap(provider).(*Provider)
	if !ok {
		return nil, fmt.Errorf("%s: computer use needs a Gemini provider, got %T", providerName, provider)
	}
	return &computerSession{provider: p, opts: opts}, nil
}

func (s *computerSession) ImageLimit() computeruse.ImageLimit {
	return computerImageLimit
}

func (s *computerSession) Next(ctx context.Context, obs computeruse.Observation) (*computeruse.Turn, error) {
	s.contents = append(s.contents, map[string]any{"role": "user", "parts": s.observationParts(obs)})

	body, err := json.Marshal(s.request())
	if err != nil {
		return nil, err
	}
	respBody, err := s.provider.doRequest(ctx, fmt.Sprintf(generateContentPath, s.opts.Model), body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = respBody.Close() }()

	var resp computerResponse
	if err := json.NewDecoder(respBody).Decode(&resp); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode response: %w", err))
	}
	if len(resp.Candidates) == 0 {
		reason := "no candidates"
		if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
			reason = "blocked: " + resp.PromptFeedback.BlockReason
		}
		return nil, llm.WrapError(providerName, errors.New(reason))
	}
	candidate := resp.Candidates[0]
	s.contents = append(s.contents, candidate.Content)
	return s.turn(candidate.Content, obs.Screen, resp.UsageMetadata)
}

func (s *computerSession) request() map[string]any {
	instructions := computerInstructions + computeruse.DoneInstruction
	if s.opts.System != "" {
		instructions += "\n\n" + s.opts.System
	}
	request := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": instructions}}},
		"contents":          s.contents,
		"tools": []any{
			map[string]any{"computerUse": map[string]any{
				"environment":                 desktopEnvironment,
				"excludedPredefinedFunctions": browserOnlyFunctions,
			}},
			map[string]any{"functionDeclarations": []any{map[string]any{
				"name":        computeruse.DoneToolName,
				"description": computeruse.DoneToolDescription,
				"parameters":  computeruse.DoneParameters(),
			}}},
		},
	}
	if s.opts.MaxTokens != nil {
		request["generationConfig"] = map[string]any{"maxOutputTokens": *s.opts.MaxTokens}
	}
	return request
}

// observationParts answers every pending function call; the screenshot is
// attached to the last answer, or sent on its own when nothing is pending.
func (s *computerSession) observationParts(obs computeruse.Observation) []any {
	failures := map[string]string{}
	for _, result := range obs.Results {
		if failures[result.CallID] != "" {
			continue
		}
		if result.Error != "" {
			failures[result.CallID] = result.Error
		} else if result.Skipped {
			failures[result.CallID] = computeruse.SkippedText
		}
	}
	var parts []any
	for i, call := range s.pending {
		response := map[string]any{}
		switch {
		case call.err != nil:
			response["error"] = call.err.Error()
		case failures[call.key] != "":
			response["error"] = failures[call.key]
		}
		if call.confirm && obs.Acknowledged {
			response["safety_acknowledgement"] = "true"
		}
		functionResponse := map[string]any{"name": call.name, "response": response}
		if call.id != "" {
			functionResponse["id"] = call.id
		}
		if i == len(s.pending)-1 && call.name != computeruse.DoneToolName {
			functionResponse["parts"] = []any{inlineImage(obs.Screen.Image)}
		}
		parts = append(parts, map[string]any{"functionResponse": functionResponse})
	}

	var text string
	if len(s.contents) == 0 {
		text = "Task: " + s.opts.Task + "\n\n"
	}
	if obs.Note != "" {
		text += obs.Note + "\n\n"
	}
	if len(s.pending) == 0 {
		text += fmt.Sprintf("Current screen (%dx%d pixels).", obs.Screen.Width, obs.Screen.Height)
		parts = append(parts, map[string]any{"text": text}, inlineImage(obs.Screen.Image))
	} else if text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	return parts
}

func inlineImage(image llm.Image) map[string]any {
	return map[string]any{"inlineData": map[string]any{"mimeType": image.MediaType, "data": image.Base64()}}
}

func (s *computerSession) turn(content json.RawMessage, screen computeruse.Screen, usage *usageMetadata) (*computeruse.Turn, error) {
	var parsed struct {
		Parts []struct {
			Text         string `json:"text"`
			Thought      bool   `json:"thought"`
			FunctionCall *struct {
				ID   string          `json:"id"`
				Name string          `json:"name"`
				Args json.RawMessage `json:"args"`
			} `json:"functionCall"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		return nil, llm.WrapError(providerName, fmt.Errorf("failed to decode content: %w", err))
	}

	turn := &computeruse.Turn{}
	if usage != nil {
		turn.Usage = llm.Usage{
			PromptTokens:     usage.PromptTokenCount,
			CompletionTokens: usage.CandidatesTokenCount,
			TotalTokens:      usage.TotalTokenCount,
		}
	}
	s.pending = s.pending[:0]
	var confirmations []string
	for i, part := range parsed.Parts {
		if part.FunctionCall == nil {
			if !part.Thought {
				turn.Text += part.Text
			}
			continue
		}
		call := geminiCall{id: part.FunctionCall.ID, key: part.FunctionCall.ID, name: part.FunctionCall.Name}
		if call.key == "" {
			call.key = "call-" + strconv.Itoa(i)
		}
		if call.name == computeruse.DoneToolName {
			turn.Done, call.err = computeruse.ParseDone(part.FunctionCall.Args)
		} else {
			var args geminiArgs
			if len(part.FunctionCall.Args) > 0 {
				call.err = json.Unmarshal(part.FunctionCall.Args, &args)
			}
			if call.err == nil {
				var actions []computeruse.Action
				actions, call.err = computerActions(call.key, call.name, args, screen)
				turn.Actions = append(turn.Actions, actions...)
			}
			if args.SafetyDecision != nil && args.SafetyDecision.Decision == requireConfirmation {
				call.confirm = true
				confirmations = append(confirmations, args.SafetyDecision.Explanation)
			}
		}
		s.pending = append(s.pending, call)
	}
	turn.Confirmation = strings.Join(confirmations, "\n")
	return turn, nil
}

type computerResponse struct {
	Candidates []struct {
		Content json.RawMessage `json:"content"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *usageMetadata `json:"usageMetadata"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// geminiArgs holds the arguments of every predefined computer use
// function, in both the current and the earlier naming.
type geminiArgs struct {
	X                 *int     `json:"x"`
	Y                 *int     `json:"y"`
	StartX            *int     `json:"start_x"`
	StartY            *int     `json:"start_y"`
	EndX              *int     `json:"end_x"`
	EndY              *int     `json:"end_y"`
	DestinationX      *int     `json:"destination_x"`
	DestinationY      *int     `json:"destination_y"`
	Text              string   `json:"text"`
	PressEnter        bool     `json:"press_enter"`
	ClearBeforeTyping bool     `json:"clear_before_typing"`
	Direction         string   `json:"direction"`
	Magnitude         int      `json:"magnitude"`
	MagnitudeInPixels int      `json:"magnitude_in_pixels"`
	Key               string   `json:"key"`
	Keys              keyList  `json:"keys"`
	Seconds           *float64 `json:"seconds"`
	SafetyDecision    *struct {
		Decision    string `json:"decision"`
		Explanation string `json:"explanation"`
	} `json:"safety_decision"`
}

// keyList accepts keys as a list or as a "+"-joined combination.
type keyList []string

func (k *keyList) UnmarshalJSON(data []byte) error {
	var combo string
	if err := json.Unmarshal(data, &combo); err == nil {
		*k = computeruse.SplitKeys(combo)
		return nil
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	*k = keys
	return nil
}

// computerActions converts a predefined function call into the actions
// that perform it. Positions arrive on a 1000-point grid and are scaled to
// the screenshot.
func computerActions(callID, name string, args geminiArgs, screen computeruse.Screen) ([]computeruse.Action, error) {
	point := func(x, y *int) (*computeruse.Point, error) {
		if x == nil || y == nil {
			return nil, fmt.Errorf("%s needs a position", name)
		}
		return &computeruse.Point{X: *x * screen.Width / normalizedScale, Y: *y * screen.Height / normalizedScale}, nil
	}
	action := computeruse.Action{CallID: callID}
	var err error
	switch name {
	case "click", "click_at", "double_click", "right_click":
		action.Kind = computeruse.KindClick
		action.Button, action.Count = computeruse.ButtonLeft, 1
		switch name {
		case "double_click":
			action.Count = 2
		case "right_click":
			action.Button = computeruse.ButtonRight
		}
		action.Point, err = point(args.X, args.Y)
	case "hover_at":
		action.Kind = computeruse.KindMove
		action.Point, err = point(args.X, args.Y)
	case "type", "type_text_at":
		return typeActions(callID, args, point)
	case "drag_and_drop":
		var start, end *computeruse.Point
		if args.StartX != nil {
			start, err = point(args.StartX, args.StartY)
		} else {
			start, err = point(args.X, args.Y)
		}
		if err != nil {
			return nil, err
		}
		if args.EndX != nil {
			end, err = point(args.EndX, args.EndY)
		} else {
			end, err = point(args.DestinationX, args.DestinationY)
		}
		if err != nil {
			return nil, err
		}
		action.Kind = computeruse.KindDrag
		action.Path = []computeruse.Point{*start, *end}
	case "scroll", "scroll_at", "scroll_document":
		action.Kind = computeruse.KindScroll
		if name != "scroll_document" {
			if action.Point, err = point(args.X, args.Y); err != nil {
				return nil, err
			}
		}
		amount := computeruse.PixelsToNotches(max(args.MagnitudeInPixels, args.Magnitude))
		if amount == 0 {
			amount = defaultScrollNotches
		}
		action.ScrollX, action.ScrollY, err = computeruse.ScrollDelta(args.Direction, amount)
	case "press_key":
		action.Kind = computeruse.KindKey
		action.Keys = computeruse.SplitKeys(args.Key)
	case "hotkey", "key_combination":
		action.Kind = computeruse.KindKey
		action.Keys = args.Keys
	case "wait", "wait_5_seconds":
		action.Kind = computeruse.KindWait
		action.Duration = defaultWaitDuration
		if args.Seconds != nil {
			action.Duration = time.Duration(*args.Seconds * float64(time.Second))
		}
	case "take_screenshot":
		action.Kind = computeruse.KindScreenshot
	default:
		return nil, fmt.Errorf("unsupported computer use function %q", name)
	}
	if err != nil {
		return nil, err
	}
	if action.Kind == computeruse.KindKey && len(action.Keys) == 0 {
		return nil, fmt.Errorf("%s needs keys", name)
	}
	return []computeruse.Action{action}, nil
}

// typeActions types at the focus, or clicks the given position first. A
// triple click selects the field's text so typing replaces it.
func typeActions(callID string, args geminiArgs, point func(x, y *int) (*computeruse.Point, error)) ([]computeruse.Action, error) {
	var actions []computeruse.Action
	if args.X != nil || args.Y != nil {
		at, err := point(args.X, args.Y)
		if err != nil {
			return nil, err
		}
		clicks := 1
		if args.ClearBeforeTyping {
			clicks = 3
		}
		actions = append(actions, computeruse.Action{CallID: callID, Kind: computeruse.KindClick, Point: at, Button: computeruse.ButtonLeft, Count: clicks})
	}
	actions = append(actions, computeruse.Action{CallID: callID, Kind: computeruse.KindType, Text: args.Text})
	if args.PressEnter {
		actions = append(actions, computeruse.Action{CallID: callID, Kind: computeruse.KindKey, Keys: []string{"Return"}})
	}
	return actions, nil
}
