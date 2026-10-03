// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"strings"
	"sync"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

var (
	_ executor.Executor                = (*extractExecutor)(nil)
	_ executor.DeclaredOutputsProvider = (*extractExecutor)(nil)
	_ executor.ExitCoder               = (*extractExecutor)(nil)
)

const errMissingModel = "xlsx.extract needs a model: set llm at the DAG level or with.llm on the step"

// Sources an extraction's cells can come from.
const (
	sourceModel = "model"
	sourceCache = "cache"
)

// extractSystemPrompt tells the model what the listing is and what to
// answer: addresses, never values.
const extractSystemPrompt = "You locate fields on a spreadsheet form. The user message lists the non-empty cells of one sheet, " +
	"one per line, as ADDRESS [KIND,HINTS]: TEXT; a merged region is listed once by its range, such as A1:D1. " +
	"For each requested field, answer with the address of the single cell that holds the field's value, " +
	"not the cell holding its label; a value usually sits to the right of or below its label. " +
	"Answer null for a field the sheet does not have. In labels, list the addresses of the cells that are labels or " +
	"headings rather than values: field names, column headings, and the form's title. Answer only addresses, never " +
	"values, by calling the " + agentstep.RespondToolName + " tool."

// cellAddressPattern is the one form an answer may take: an A1 address,
// with or without a sheet.
var cellAddressPattern = regexp.MustCompile(`^(?:'(?:[^']|'')+'!|[^!'\s]+!)?\$?[A-Za-z]{1,3}\$?[0-9]+$`)

// labelPattern is the form a named label may take: a cell address, or the
// span of a merged region such as A1:D1, with or without a sheet.
var labelPattern = regexp.MustCompile(`^(?:'(?:[^']|'')+'!|[^!'\s]+!)?\$?[A-Za-z]{1,3}\$?[0-9]+(?::\$?[A-Za-z]{1,3}\$?[0-9]+)?$`)

// respondLabels is the respond tool property that lists the label cells.
const respondLabels = "labels"

// providerFactory builds a provider for one resolved model configuration.
type providerFactory func(ctx context.Context, cfg *ir.LLMConfig) (llmpkg.Provider, error)

// model is one configured model; its provider is built when its turn
// comes, so a model that cannot be built is passed over like one that
// fails to answer.
type model struct {
	cfg          *ir.LLMConfig
	providerType llmpkg.ProviderType
}

func (m model) label() string {
	return string(m.providerType) + "/" + m.cfg.Model
}

// tokenUsage counts model tokens.
type tokenUsage struct {
	Input  int
	Output int
}

func (u tokenUsage) total() int { return u.Input + u.Output }

func (u *tokenUsage) add(usage llmpkg.Usage) {
	u.Input += usage.PromptTokens
	u.Output += usage.CompletionTokens
}

// extractExecutor runs xlsx.extract: it lists the sheet for a model, asks
// which cells hold the requested fields, reads those cells, and keeps the
// answer by sheet layout so a form seen before needs no model call.
type extractExecutor struct {
	stdout io.Writer
	stderr io.Writer
	step   ir.Step
	path   string
	cfg    config

	dataDir string
	dagName string
	secrets map[string]string
	masker  *masking.Masker
	// models are resolved on the first model request and each provider is
	// built when its model's turn comes, so a run answered from the cache
	// never touches a provider and a fallback that cannot be built does
	// not stop the models before it.
	models []model
	cache  *extractCache
	// parameters is the respond tool's schema, and schemaDigest identifies
	// it, so a cached answer is reused only for the same fields, types, and
	// descriptions.
	parameters   map[string]any
	schemaDigest string
	// newProvider builds providers; tests replace it.
	newProvider providerFactory

	mu       sync.Mutex
	cancel   context.CancelFunc
	outputs  map[string]any
	usage    tokenUsage
	exitCode int
}

func newExtractExecutor(ctx context.Context, env runtime.Env, step ir.Step, path string, cfg config) (*extractExecutor, error) {
	if step.LLM == nil {
		return nil, errors.New(errMissingModel)
	}
	dataDir := cmnconfig.GetConfig(ctx).Paths.DataDir
	if dataDir == "" {
		return nil, errors.New("xlsx: the Dagu data directory is not configured")
	}
	var secrets map[string]string
	if env.Scope != nil {
		secrets = env.Scope.AllSecrets()
	}
	if err := agentstep.CheckTextSecrets(executorType, "instruction", cfg.Instruction, secrets); err != nil {
		return nil, err
	}
	// Property descriptions reach the model inside the respond tool, which
	// is not masked, so a secret in one is refused the same way.
	properties, _ := cfg.Schema["properties"].(map[string]any)
	for _, name := range cfg.extractProperties {
		property, _ := properties[name].(map[string]any)
		description, _ := property["description"].(string)
		if err := agentstep.CheckTextSecrets(executorType, "schema.properties."+name+".description", description, secrets); err != nil {
			return nil, err
		}
	}
	dagName := ""
	if env.DAG != nil {
		dagName = env.DAG.Name
	}
	parameters := agentstep.ToolParameters(responseSchema(cfg.Schema, cfg.extractProperties))
	// The digest covers what the model is asked and how each answer is
	// read: the tool parameters carry names and descriptions, the request
	// schema's properties carry the types and formats that pin the cells.
	canonical, err := json.Marshal(map[string]any{"parameters": parameters, "properties": properties})
	if err != nil {
		return nil, fmt.Errorf("xlsx: encode the response schema: %w", err)
	}
	sum := sha256.Sum256(canonical)
	e := &extractExecutor{
		stdout:       os.Stdout,
		stderr:       os.Stderr,
		step:         step,
		path:         path,
		cfg:          cfg,
		dataDir:      dataDir,
		dagName:      dagName,
		secrets:      secrets,
		masker:       agentstep.NewMasker(secrets, nil),
		parameters:   parameters,
		schemaDigest: hex.EncodeToString(sum[:]),
	}
	return e, nil
}

func (e *extractExecutor) SetStdout(out io.Writer) { e.stdout = out }
func (e *extractExecutor) SetStderr(out io.Writer) { e.stderr = out }

func (e *extractExecutor) Kill(os.Signal) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *extractExecutor) ExitCode() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.exitCode
}

func (e *extractExecutor) GetOutputs() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return maps.Clone(e.outputs)
}

func (e *extractExecutor) PublishesDeclaredOutputs() bool { return true }

func (e *extractExecutor) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()

	outputs, line, warnings, err := e.run(ctx)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.exitCode = 1
		return err
	}
	e.outputs = outputs
	for _, w := range warnings {
		_, _ = fmt.Fprintln(e.stderr, "warning: "+w)
	}
	_, _ = fmt.Fprintln(e.stdout, line)
	return nil
}

// run lists the sheet, finds the cells from the cache or the model, reads
// them, and returns the outputs, the stdout line, and the warnings.
func (e *extractExecutor) run(ctx context.Context) (map[string]any, string, []string, error) {
	if e.cfg.Cache {
		stepKey := e.step.ID
		if stepKey == "" {
			stepKey = e.step.Name
		}
		e.cache = openExtractCache(e.dataDir, e.dagName, stepKey)
	}

	layout, err := workbook.Layout(ctx, e.path, workbook.LayoutOptions{
		Password:   e.cfg.Password,
		Sheet:      e.cfg.Sheet,
		Range:      e.cfg.Range,
		SendValues: e.cfg.SendValues,
		Trim:       e.cfg.Trim,
		Formulas:   workbook.FormulaMode(e.cfg.Formulas),
	})
	if err != nil {
		return nil, "", nil, err
	}

	result, source, err := e.locate(ctx, layout)
	if err != nil {
		if e.cache != nil {
			e.cache.Discard()
		}
		return nil, "", nil, err
	}
	warnings := append([]string{}, result.Warnings...)
	if e.cache != nil {
		if err := e.cache.Commit(ctx); err != nil {
			warnings = append(warnings, "keep extract recordings: "+err.Error())
		}
	}

	outputs := make(map[string]any, len(e.cfg.extractProperties)+len(extractFixedOutputs))
	for _, field := range e.cfg.extractProperties {
		outputs[field] = result.Values[field]
	}
	outputs["cells"] = result.Cells
	outputs["sheet"] = result.Sheet
	outputs["warnings"] = warnings
	outputs["source"] = source

	line := fmt.Sprintf("Extracted %d %s from %s %s (%s)", len(e.cfg.extractProperties), plural(len(e.cfg.extractProperties), "field"),
		workbook.Base(e.path), result.Sheet, e.sourceNote(source))
	return outputs, line, warnings, nil
}

func (e *extractExecutor) sourceNote(source string) string {
	if source == sourceCache {
		return sourceCache
	}
	return fmt.Sprintf("%s, %d tokens", sourceModel, e.usage.total())
}

// locate finds the cell of every field: from a recording that serves this
// sheet, else from the model, whose answer is then recorded. source says
// which. A recording of the sheet's own layout serves it when its
// instruction, schema, and labels hold; one of another layout serves it
// when, besides, it saw every cell the sheet lists, which is the same
// template with a box left blank.
func (e *extractExecutor) locate(ctx context.Context, layout *workbook.SheetLayout) (*workbook.ReadCellsResult, string, error) {
	if e.cache != nil {
		if entry, ok := e.cache.Lookup(layout.Key); ok {
			if e.serves(entry, layout) {
				result, err := e.readCells(ctx, layout, entry.Cells)
				if err == nil {
					return result, sourceCache, nil
				}
				if _, bad := errors.AsType[*workbook.AddressError](err); !bad {
					return nil, "", err
				}
			}
			// The form changed under the recording, or the fields did; the
			// model is asked again and its answer takes the recording's
			// place once the step succeeds.
			e.cache.Drop(layout.Key)
		}
		if _, entry, ok := e.cache.Find(func(key string, entry extractEntry) bool {
			return key != layout.Key && e.serves(entry, layout) && listedCovers(entry.Listed, layout.Addresses)
		}); ok {
			result, err := e.readCells(ctx, layout, entry.Cells)
			if err == nil {
				return result, sourceCache, nil
			}
			if _, bad := errors.AsType[*workbook.AddressError](err); !bad {
				return nil, "", err
			}
			// A recorded cell lies outside this sheet's range; the recording
			// still serves its own layout, so the model is asked for this one.
		}
	}
	result, labels, err := e.query(ctx, layout)
	if err != nil {
		return nil, "", err
	}
	if e.cache != nil {
		e.cache.Stage(layout.Key, extractEntry{
			Layout:      layout.Key,
			Instruction: e.cfg.Instruction,
			Schema:      e.schemaDigest,
			Cells:       result.Cells,
			Labels:      labelsFor(result.Cells, labels, layout.Labels),
			Listed:      layout.Addresses,
		})
	}
	return result, sourceModel, nil
}

// serves reports whether a recording answers this sheet: the same
// instruction and schema, and every watched label still reading the same.
func (e *extractExecutor) serves(entry extractEntry, layout *workbook.SheetLayout) bool {
	return entry.Instruction == e.cfg.Instruction && entry.Schema == e.schemaDigest && labelsHold(entry.Labels, layout.Labels)
}

func (e *extractExecutor) readCells(ctx context.Context, layout *workbook.SheetLayout, cells map[string]string) (*workbook.ReadCellsResult, error) {
	return workbook.ReadCells(ctx, e.path, workbook.ReadCellsOptions{
		Password: e.cfg.Password,
		Sheet:    layout.Sheet,
		// The listed range bounds the answers, whether it was configured
		// or derived from the used range.
		Range:    layout.Range,
		Cells:    cells,
		Types:    e.cfg.extractTypes,
		Trim:     e.cfg.Trim,
		Formulas: workbook.FormulaMode(e.cfg.Formulas),
	})
}

// query asks the models, in order, which cell holds each field, reads the
// cells the first usable answer names, and returns what they hold and the
// label cells the answer named. A model
// that cannot be built, whose request fails, or whose answer is not
// addresses within the listed sheet and range is passed over for the next
// one; a cell that fails its pinned type fails the step, since another
// model would read the same cell.
func (e *extractExecutor) query(ctx context.Context, layout *workbook.SheetLayout) (*workbook.ReadCellsResult, []string, error) {
	if err := e.ensureModels(ctx); err != nil {
		return nil, nil, err
	}
	factory := e.newProvider
	if factory == nil {
		factory = runtime.NewLLMProvider
	}
	user := e.cfg.Instruction + "\n\nSheet: " + layout.Sheet + " (" + layout.Range + ", " +
		fmt.Sprintf("%d %s", layout.Cells, plural(layout.Cells, "cell")) + ")\n" + layout.Listing
	messages := []llmpkg.Message{
		{Role: llmpkg.RoleSystem, Content: extractSystemPrompt},
		{Role: llmpkg.RoleUser, Content: e.masker.MaskString(user)},
	}
	var errs []error
	for _, m := range e.models {
		provider, err := factory(ctx, m.cfg)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
			continue
		}
		resp, err := llmpkg.ChatWithRetry(ctx, provider, &llmpkg.ChatRequest{
			Model:       m.cfg.Model,
			Messages:    messages,
			Temperature: m.cfg.Temperature,
			MaxTokens:   m.cfg.MaxTokens,
			TopP:        m.cfg.TopP,
			Tools: []llmpkg.Tool{{
				Type: "function",
				Function: llmpkg.ToolFunction{
					Name:        agentstep.RespondToolName,
					Description: agentstep.RespondToolDescription,
					Parameters:  e.parameters,
				},
			}},
			ToolChoice: "required",
		}, llmpkg.DefaultLogicalRetryConfig())
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
			continue
		}
		e.usage.add(resp.Usage)
		answer, err := agentstep.StructuredAnswer(resp)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
			continue
		}
		cells, labels, err := parseAnswer(answer, e.cfg.extractProperties)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.label(), err))
			continue
		}
		result, err := e.readCells(ctx, layout, cells)
		if err != nil {
			if bad, ok := errors.AsType[*workbook.AddressError](err); ok {
				errs = append(errs, fmt.Errorf("%s: xlsx: model answered field %q with %q, which %s", m.label(), bad.Field, bad.Address, bad.Msg))
				continue
			}
			return nil, nil, err
		}
		return result, labels, nil
	}
	return nil, nil, fmt.Errorf("model request failed: %w", errors.Join(errs...))
}

// ensureModels resolves the configured models on the first request, so a
// run that reads its cells from the cache needs no provider and no key.
func (e *extractExecutor) ensureModels(ctx context.Context) error {
	if e.models != nil {
		return nil
	}
	models, err := resolveModels(ctx, e.step.LLM)
	if err != nil {
		return err
	}
	e.models = models
	return nil
}

// resolveModels lists the configured models in fallback order, without
// building their providers.
func resolveModels(ctx context.Context, cfg *ir.LLMConfig) ([]model, error) {
	entries, err := runtime.ResolveModels(ctx, cfg.GetModels())
	if err != nil {
		return nil, err
	}
	models := make([]model, 0, len(entries))
	for _, entry := range entries {
		providerType, err := llmpkg.ParseProviderType(entry.Provider)
		if err != nil {
			return nil, err
		}
		models = append(models, model{cfg: runtime.EffectiveLLMConfig(cfg, entry), providerType: providerType})
	}
	return models, nil
}

// responseSchema is what the model answers with: for every field of the
// request schema, a cell address or null.
func responseSchema(schema map[string]any, fields []string) map[string]any {
	properties := make(map[string]any, len(fields))
	requested, _ := schema["properties"].(map[string]any)
	required := make([]any, 0, len(fields))
	for _, field := range fields {
		description := field
		if spec, ok := requested[field].(map[string]any); ok {
			if d, ok := spec["description"].(string); ok && strings.TrimSpace(d) != "" {
				description = d
			}
		}
		properties[field] = map[string]any{
			"type":        []any{"string", "null"},
			"description": "The address of the cell holding " + description + ", or null when the sheet does not have it",
		}
		required = append(required, field)
	}
	properties[respondLabels] = map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": "The addresses of the cells that are labels or headings rather than values: field names, column headings, the form's title",
	}
	required = append(required, respondLabels)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}

// parseAnswer reads the model's answer: every field as a cell address, or
// null for one the sheet lacks, and the label cells it named. A field the
// model left out is absent, labels left out are none, and anything that
// is not an address is refused.
func parseAnswer(raw json.RawMessage, fields []string) (map[string]string, []string, error) {
	var answer map[string]any
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, nil, fmt.Errorf("xlsx: model answer is not an object: %w", err)
	}
	if answer == nil {
		return nil, nil, errors.New("xlsx: model answer is not an object: null")
	}
	cells := make(map[string]string, len(fields))
	for _, field := range fields {
		value, ok := answer[field]
		if !ok || value == nil {
			cells[field] = ""
			continue
		}
		text, isText := value.(string)
		text = strings.TrimSpace(text)
		if !isText || !cellAddressPattern.MatchString(text) {
			return nil, nil, fmt.Errorf("xlsx: model answered field %q with %q, not a cell address", field, fmt.Sprint(value))
		}
		cells[field] = text
	}
	labels, err := answerLabels(answer[respondLabels])
	if err != nil {
		return nil, nil, err
	}
	return cells, labels, nil
}

// answerLabels reads the label cells the model named: none when the
// answer has no labels, else every item as a cell address or a merged
// span.
func answerLabels(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("xlsx: model answered labels with %q, not a list of cell addresses", fmt.Sprint(value))
	}
	labels := make([]string, 0, len(list))
	for _, item := range list {
		text, isText := item.(string)
		text = strings.TrimSpace(text)
		if !isText || !labelPattern.MatchString(text) {
			return nil, fmt.Errorf("xlsx: model named label %q, which is not a cell address", fmt.Sprint(item))
		}
		labels = append(labels, text)
	}
	return labels, nil
}
