// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/goccy/go-yaml"
)

// sessionStateVersion is the version of the state a session's record
// carries.
const sessionStateVersion = 1

// maxSessionOperations bounds the history a session keeps.
const maxSessionOperations = 1000

// Session operation outcomes.
const (
	OperationDone    = "done"
	OperationFailed  = "failed"
	OperationSkipped = "skipped"
)

// sessionState is what a browser session remembers between commands. It
// never holds a variable's value.
type sessionState struct {
	Version     int            `json:"version"`
	CreatedAt   time.Time      `json:"createdAt"`
	LastUsedAt  time.Time      `json:"lastUsedAt"`
	IdleTimeout string         `json:"idleTimeout"`
	StartURL    string         `json:"startUrl,omitempty"`
	Browser     browserOptions `json:"browser"`
	LLM         *ir.LLMConfig  `json:"llm,omitempty"`
	// EnvVariables maps each variable read from the environment to the
	// environment variable that holds its value.
	EnvVariables map[string]string `json:"envVariables,omitempty"`
	Page         sessionPage       `json:"page"`
	Usage        sessionUsage      `json:"usage"`
	Ops          []sessionOp       `json:"ops"`
	// WriteGate is the write gate mode the browser runs in, empty without
	// a gate; WriteSeq is the last of its events reported, and Person
	// whether a person holds the browser.
	WriteGate string `json:"writeGate,omitempty"`
	WriteSeq  int64  `json:"writeSeq,omitempty"`
	Person    bool   `json:"person,omitempty"`
}

type sessionPage struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Refused is the refusal the page showed, if any.
	Refused *refusal `json:"refused,omitempty"`
}

type sessionUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// sessionOp is one operation a session ran.
type sessionOp struct {
	Kind string `json:"kind"`
	// Op is the operation as given, without its variables' values.
	Op     json.RawMessage `json:"op"`
	Status string          `json:"status"`
	// Error is why the operation failed, with secrets masked.
	Error     string    `json:"error,omitempty"`
	URLBefore string    `json:"urlBefore,omitempty"`
	URLAfter  string    `json:"urlAfter,omitempty"`
	At        time.Time `json:"at"`
	// Actions are the actions an act performed.
	Actions []recordedAction `json:"actions,omitempty"`
	// Recordable reports that the act's actions may be replayed by a step:
	// the act was recorded and its actions hold no variable's value.
	Recordable bool `json:"recordable,omitempty"`
	// Assumed is what the act's actions took for granted about the values
	// of the variables they never use, which the step's recording keeps.
	Assumed *agentstep.Assumed `json:"assumed,omitempty"`
	// Outputs names the values an extract read.
	Outputs []string `json:"outputs,omitempty"`
	// Variables names the variables the operation used.
	Variables []string `json:"variables,omitempty"`
	// Writes are the signatures of the writes of the work the operation
	// sent or tried to send, which an export keeps as commit points.
	Writes []string `json:"writes,omitempty"`
}

func (s sessionState) idleTimeout() time.Duration {
	if d, err := time.ParseDuration(s.IdleTimeout); err == nil && d > 0 {
		return d
	}
	return DefaultSessionIdleTimeout
}

func (s sessionState) modelLabel() string {
	if s.LLM == nil {
		return ""
	}
	models := s.LLM.GetModels()
	if len(models) == 0 {
		return ""
	}
	return models[0].Provider + "/" + models[0].Name
}

func decodeSessionState(data json.RawMessage) (sessionState, error) {
	var state sessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("read the session's state: %w", err)
	}
	if state.Version != sessionStateVersion {
		return state, fmt.Errorf("the session was opened by another version of Dagu (state version %d)", state.Version)
	}
	return state, nil
}

func (s sessionState) encode() (json.RawMessage, error) {
	return json.Marshal(s)
}

// SessionVariable is the value of a variable an operation uses: a literal,
// used for one command and never kept, or the name of an environment
// variable, read on every command and treated as a secret.
type SessionVariable struct {
	Value string `json:"value,omitempty"`
	Env   string `json:"env,omitempty"`
}

// UnmarshalJSON accepts a literal string or an object naming an
// environment variable, {"env": NAME}.
func (v *SessionVariable) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		*v = SessionVariable{Value: value}
		return nil
	}
	var ref struct {
		Env string `json:"env"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ref); err != nil || ref.Env == "" {
		return errors.New(`a variable is a string or {"env": "NAME"}`)
	}
	*v = SessionVariable{Env: ref.Env}
	return nil
}

// ParseSessionInput reads one operation, in the form of an item of a
// browser step's with.do, and the values of the variables it uses, from
// JSON or YAML:
//
//	{"act": "Type %password% into the Password field",
//	 "variables": {"password": {"env": "PORTAL_PASSWORD"}}}
func ParseSessionInput(data []byte) (json.RawMessage, map[string]SessionVariable, error) {
	var input map[string]any
	if err := yaml.Unmarshal(data, &input); err != nil {
		return nil, nil, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("read the operation: %v", err)}
	}
	if len(input) == 0 {
		return nil, nil, &SessionError{Code: CodeInvalidInput, Message: "give one operation, such as {\"act\": \"Click Sign in\"}"}
	}
	var variables map[string]SessionVariable
	if raw, ok := input["variables"]; ok {
		delete(input, "variables")
		data, err := json.Marshal(raw)
		if err == nil {
			err = json.Unmarshal(data, &variables)
		}
		if err != nil {
			return nil, nil, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("variables: %v", err)}
		}
	}
	op, err := json.Marshal(input)
	if err != nil {
		return nil, nil, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("read the operation: %v", err)}
	}
	return op, variables, nil
}

// parseSessionOperation checks one operation as a browser step would check
// it in with.do. A session cannot ask a person for input, since it has
// one at the command line.
func parseSessionOperation(raw json.RawMessage) (operation, error) {
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		return operation{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("read the operation: %v", err)}
	}
	if _, ok := item[opAsk]; ok {
		return operation{}, &SessionError{Code: CodeInvalidInput, Message: "a browser session cannot ask for input; run the operation the answer is for instead"}
	}
	if err := registry.ValidateExecutorConfig(executorType, map[string]any{"do": []any{item}}); err != nil {
		return operation{}, &SessionError{Code: CodeInvalidInput, Message: err.Error()}
	}
	var op operation
	if err := json.Unmarshal(raw, &op); err != nil {
		return operation{}, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("read the operation: %v", err)}
	}
	if err := op.validate(); err != nil {
		return operation{}, &SessionError{Code: CodeInvalidInput, Message: err.Error()}
	}
	return op, nil
}

// needsModel reports whether the operation asks the model.
func (o operation) needsModel() bool {
	return o.Act != nil || o.Extract != nil || (o.Upload != nil && o.Upload.Into != "") ||
		(o.Expect != nil && o.Expect.judged()) || (o.When != nil && o.When.judged())
}

// sessionValues are the variables a command's operation can use.
type sessionValues struct {
	// all holds every variable's value.
	all map[string]string
	// secrets holds the values read from the environment.
	secrets map[string]string
}

// resolveVariables registers the environment variables given and reads
// every registered one, then adds the literal values given for this
// command.
func resolveVariables(state *sessionState, given map[string]SessionVariable) (sessionValues, error) {
	values := sessionValues{all: map[string]string{}, secrets: map[string]string{}}
	for name, variable := range given {
		if !agentstep.IdentifierPattern.MatchString(name) {
			return values, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("variable name %q must match %s", name, agentstep.IdentifierPattern)}
		}
		if variable.Env != "" {
			if state.EnvVariables == nil {
				state.EnvVariables = map[string]string{}
			}
			state.EnvVariables[name] = variable.Env
		}
	}
	for _, name := range slices.Sorted(maps.Keys(state.EnvVariables)) {
		env := state.EnvVariables[name]
		value, ok := os.LookupEnv(env)
		if !ok {
			return values, &SessionError{Code: CodeInvalidInput, Message: fmt.Sprintf("variable %s reads the environment variable %s, which is not set; "+
				"set it for every command of the session, which masks its value in what the page shows", name, env)}
		}
		values.all[name] = value
		values.secrets[name] = value
	}
	for name, variable := range given {
		if variable.Env == "" {
			values.all[name] = variable.Value
			delete(values.secrets, name)
		}
	}
	return values, nil
}

// holdsValue reports whether any argument of actions contains the value of
// a variable long enough to be told apart from ordinary text.
func holdsValue(actions []recordedAction, values map[string]string) bool {
	for _, action := range actions {
		for _, argument := range action.Arguments {
			for _, value := range values {
				if len([]rune(value)) >= minRecordedValueRunes && strings.Contains(argument, value) {
					return true
				}
			}
		}
	}
	return false
}

// minRecordedValueRunes is the shortest variable value whose presence in a
// recorded action keeps the action from being replayed.
const minRecordedValueRunes = 4
