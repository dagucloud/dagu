// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/llm"
)

// Generic function tool names.
const (
	genericClick  = "click"
	genericMove   = "move"
	genericDrag   = "drag"
	genericScroll = "scroll"
	genericType   = "type"
	genericKey    = "key"
	genericWait   = "wait"
)

// The done tool is how a model reports the task finished, in every mode.
const (
	DoneToolName        = "done"
	DoneToolDescription = "Report that the task is finished or cannot be finished."
	// DoneInstruction tells the model how to end a task.
	DoneInstruction = "When the task is complete, or cannot be completed, call done with success set accordingly and a short summary."
)

const (
	defaultScrollNotches = 3
	// keptScreenshots is how many of the latest screenshots stay in the
	// conversation; older ones are replaced with a note to bound its size.
	keptScreenshots   = 3
	omittedScreenshot = "(An earlier screenshot was removed from the conversation.)"
)

// genericImageLimit fits the image limits of common vision models.
var genericImageLimit = ImageLimit{LongEdge: 1568, MaxPixels: 1_150_000}

const genericSystemPrompt = `You operate a computer by calling tools. After each round of tool calls you receive a screenshot of the screen. Coordinates are pixels in that screenshot, measured from its top-left corner.
Work in small steps and check each new screenshot before continuing. Key names follow common spelling, such as "Return", "Tab", "ctrl+c" or "cmd+space".
` + DoneInstruction

// genericSession drives a model through plain function tools.
type genericSession struct {
	provider llm.Provider
	opts     Options
	messages []llm.Message
	started  bool
	// pending are the tool calls of the last turn, which the next
	// observation must answer.
	pending []pendingCall
}

// pendingCall is a tool call awaiting its result. err is set when the call
// could not be turned into an action.
type pendingCall struct {
	id   string
	name string
	err  error
}

func newGenericSession(provider llm.Provider, opts Options) *genericSession {
	return &genericSession{provider: provider, opts: opts}
}

func (s *genericSession) ImageLimit() ImageLimit {
	return genericImageLimit
}

func (s *genericSession) Next(ctx context.Context, obs Observation) (*Turn, error) {
	s.appendObservation(obs)
	resp, err := llm.ChatWithRetry(ctx, s.provider, &llm.ChatRequest{
		Model:       s.opts.Model,
		Messages:    s.messages,
		Temperature: s.opts.Temperature,
		MaxTokens:   s.opts.MaxTokens,
		Tools:       genericTools,
		ToolChoice:  "required",
	}, llm.DefaultLogicalRetryConfig())
	if err != nil {
		return nil, err
	}
	s.messages = append(s.messages, llm.Message{
		Role:      llm.RoleAssistant,
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})

	turn := &Turn{Text: resp.Content, Usage: resp.Usage}
	s.pending = s.pending[:0]
	// Actions after a call that cannot be performed are skipped, as when an
	// action fails.
	halted := false
	for _, call := range resp.ToolCalls {
		pending := pendingCall{id: call.ID, name: call.Function.Name}
		switch {
		case call.Function.Name == DoneToolName:
			turn.Done, pending.err = ParseDone([]byte(call.Function.Arguments))
		case !halted:
			var action Action
			if action, pending.err = parseGenericAction(call); pending.err == nil {
				turn.Actions = append(turn.Actions, action)
			} else {
				halted = true
			}
		}
		s.pending = append(s.pending, pending)
	}
	return turn, nil
}

// appendObservation answers the pending tool calls and adds the screenshot.
func (s *genericSession) appendObservation(obs Observation) {
	if !s.started {
		system := genericSystemPrompt
		if s.opts.System != "" {
			system += "\n\n" + s.opts.System
		}
		s.messages = append(s.messages, llm.Message{Role: llm.RoleSystem, Content: system})
	}

	results := make(map[string]Result, len(obs.Results))
	for _, result := range obs.Results {
		results[result.CallID] = result
	}
	for _, call := range s.pending {
		content := Result{Skipped: true}.Text()
		if call.err != nil {
			content = "Error: " + call.err.Error()
		} else if result, ok := results[call.id]; ok {
			content = result.Text()
		} else if call.name == DoneToolName {
			content = "OK"
		}
		s.messages = append(s.messages, llm.Message{
			Role:       llm.RoleTool,
			ToolCallID: call.id,
			Name:       call.name,
			Content:    content,
		})
	}

	var text strings.Builder
	if !s.started {
		text.WriteString("Task: " + s.opts.Task + "\n\n")
		s.started = true
	}
	if obs.Note != "" {
		text.WriteString(obs.Note + "\n\n")
	}
	fmt.Fprintf(&text, "Current screen (%dx%d pixels).", obs.Screen.Width, obs.Screen.Height)
	s.dropOldScreenshots()
	s.messages = append(s.messages, llm.Message{
		Role:    llm.RoleUser,
		Content: text.String(),
		Images:  []llm.Image{obs.Screen.Image},
	})
}

// dropOldScreenshots removes images from all but the latest screenshots,
// leaving room for the one about to be added.
func (s *genericSession) dropOldScreenshots() {
	kept := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		if len(s.messages[i].Images) == 0 {
			continue
		}
		kept++
		if kept < keptScreenshots {
			continue
		}
		s.messages[i].Images = nil
		s.messages[i].Content += "\n" + omittedScreenshot
	}
}

// ParseDone decodes the arguments of a done tool call.
func ParseDone(arguments []byte) (*Done, error) {
	var done Done
	if err := json.Unmarshal([]byte(defaultArguments(string(arguments))), &done); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return &done, nil
}

// DoneParameters returns the JSON schema of the done tool's arguments.
func DoneParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"success": map[string]any{"type": "boolean", "description": "Whether the task was completed."},
			"summary": map[string]any{"type": "string", "description": "What was done, or why the task could not be completed."},
		},
		"required": []string{"success", "summary"},
	}
}

// genericArgs holds the arguments of every generic tool.
type genericArgs struct {
	X         *int     `json:"x"`
	Y         *int     `json:"y"`
	Button    string   `json:"button"`
	Count     int      `json:"count"`
	Modifiers []string `json:"modifiers"`
	StartX    *int     `json:"start_x"`
	StartY    *int     `json:"start_y"`
	EndX      *int     `json:"end_x"`
	EndY      *int     `json:"end_y"`
	Direction string   `json:"direction"`
	Amount    int      `json:"amount"`
	Text      string   `json:"text"`
	Keys      string   `json:"keys"`
	Repeat    int      `json:"repeat"`
	Seconds   float64  `json:"seconds"`
}

func parseGenericAction(call llm.ToolCall) (Action, error) {
	var args genericArgs
	if err := json.Unmarshal([]byte(defaultArguments(call.Function.Arguments)), &args); err != nil {
		return Action{}, fmt.Errorf("invalid arguments: %w", err)
	}
	action := Action{CallID: call.ID}
	switch call.Function.Name {
	case genericClick:
		point, err := requiredPoint(args.X, args.Y)
		if err != nil {
			return Action{}, err
		}
		action.Kind = KindClick
		action.Point = point
		action.Button = args.Button
		action.Count = args.Count
		action.Modifiers = args.Modifiers
	case genericMove:
		point, err := requiredPoint(args.X, args.Y)
		if err != nil {
			return Action{}, err
		}
		action.Kind = KindMove
		action.Point = point
	case genericDrag:
		start, err := requiredPoint(args.StartX, args.StartY)
		if err != nil {
			return Action{}, err
		}
		end, err := requiredPoint(args.EndX, args.EndY)
		if err != nil {
			return Action{}, err
		}
		action.Kind = KindDrag
		action.Path = []Point{*start, *end}
	case genericScroll:
		point, err := requiredPoint(args.X, args.Y)
		if err != nil {
			return Action{}, err
		}
		action.Kind = KindScroll
		action.Point = point
		amount := args.Amount
		if amount <= 0 {
			amount = defaultScrollNotches
		}
		if action.ScrollX, action.ScrollY, err = ScrollDelta(args.Direction, amount); err != nil {
			return Action{}, err
		}
	case genericType:
		action.Kind = KindType
		action.Text = args.Text
	case genericKey:
		action.Kind = KindKey
		action.Keys = SplitKeys(args.Keys)
		action.Repeat = args.Repeat
		if len(action.Keys) == 0 {
			return Action{}, fmt.Errorf("keys is required")
		}
	case genericWait:
		action.Kind = KindWait
		action.Duration = time.Duration(args.Seconds * float64(time.Second))
	default:
		return Action{}, fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	return action, nil
}

// ScrollDelta turns a direction and an amount into scroll deltas.
func ScrollDelta(direction string, amount int) (x, y int, err error) {
	switch strings.ToLower(direction) {
	case "up":
		return 0, -amount, nil
	case "down":
		return 0, amount, nil
	case "left":
		return -amount, 0, nil
	case "right":
		return amount, 0, nil
	default:
		return 0, 0, fmt.Errorf("invalid scroll direction %q", direction)
	}
}

func requiredPoint(x, y *int) (*Point, error) {
	if x == nil || y == nil {
		return nil, fmt.Errorf("x and y are required")
	}
	return &Point{X: *x, Y: *y}, nil
}

func defaultArguments(arguments string) string {
	if strings.TrimSpace(arguments) == "" {
		return "{}"
	}
	return arguments
}

var genericTools = []llm.Tool{
	genericTool(genericClick, "Click at a screen position.", map[string]any{
		"x":         integerProperty("Horizontal pixel position."),
		"y":         integerProperty("Vertical pixel position."),
		"button":    map[string]any{"type": "string", "enum": []string{ButtonLeft, ButtonRight, ButtonMiddle}, "description": "Mouse button; left by default."},
		"count":     map[string]any{"type": "integer", "minimum": 1, "maximum": 3, "description": "Number of clicks; 2 for a double click."},
		"modifiers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Keys held during the click, such as shift or ctrl."},
	}, "x", "y"),
	genericTool(genericMove, "Move the pointer without clicking.", map[string]any{
		"x": integerProperty("Horizontal pixel position."),
		"y": integerProperty("Vertical pixel position."),
	}, "x", "y"),
	genericTool(genericDrag, "Drag with the left button from one position to another.", map[string]any{
		"start_x": integerProperty("Start horizontal pixel position."),
		"start_y": integerProperty("Start vertical pixel position."),
		"end_x":   integerProperty("End horizontal pixel position."),
		"end_y":   integerProperty("End vertical pixel position."),
	}, "start_x", "start_y", "end_x", "end_y"),
	genericTool(genericScroll, "Scroll with the mouse wheel at a screen position.", map[string]any{
		"x":         integerProperty("Horizontal pixel position."),
		"y":         integerProperty("Vertical pixel position."),
		"direction": map[string]any{"type": "string", "enum": []string{"up", "down", "left", "right"}},
		"amount":    map[string]any{"type": "integer", "minimum": 1, "description": "Wheel notches; 3 by default."},
	}, "x", "y", "direction"),
	genericTool(genericType, "Type text at the keyboard focus.", map[string]any{
		"text": map[string]any{"type": "string"},
	}, "text"),
	genericTool(genericKey, "Press a key or key combination.", map[string]any{
		"keys":   map[string]any{"type": "string", "description": `Key or combination joined with "+", such as "Return" or "ctrl+s".`},
		"repeat": map[string]any{"type": "integer", "minimum": 1, "description": "Number of presses; 1 by default."},
	}, "keys"),
	genericTool(genericWait, "Wait before looking at the screen again.", map[string]any{
		"seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 60},
	}, "seconds"),
	{Type: "function", Function: llm.ToolFunction{Name: DoneToolName, Description: DoneToolDescription, Parameters: DoneParameters()}},
}

func genericTool(name, description string, properties map[string]any, required ...string) llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        name,
			Description: description,
			Parameters: map[string]any{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		},
	}
}

func integerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
