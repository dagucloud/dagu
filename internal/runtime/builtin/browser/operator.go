// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/certproxy"
	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// pendingReportAfter is how long a connection waits before an operation
	// that failed names it.
	pendingReportAfter = 5 * time.Second
)

// operator performs the operations of with.do on one browser. It reports
// what each operation did and leaves recording it to the caller.
type operator struct {
	eng            engine
	bridge         *modelBridge
	variables      map[string]string
	allowedDomains []string
	// secret reports whether the value of a variable holds a secret, which
	// messages leave out; nil counts every value as one.
	secret func(name string) bool
	// recorder replays and records acts; nil when acts do neither.
	recorder actRecorder
	// choice returns how much AI decides for an act or an upload with the
	// given settings. nil, as in a browser session, decides on_miss for
	// each and records only those whose cache is on.
	choice func(ai string, cache *bool) string
	// shots stores the screenshots screenshot operations take.
	shots *agentstep.ArtifactStore
	// workDir resolves the relative paths of the files uploads give; empty
	// means the current directory.
	workDir string
	// warn reports a problem that did not stop the operation at index.
	warn func(index int, message string)
	// certificateProxy is set when the browser's https: requests go through
	// the certificate proxy: those to the hosts it presents certificates
	// to, and, with a guard, the others.
	certificateProxy bool
	// certificateEvent reports what the certificate proxy saw while the
	// operation at index ran; nil discards it.
	certificateEvent func(index int, event certproxy.Event)
	// certificateProxyLost is set once the certificate proxy stopped
	// answering, which is reported once.
	certificateProxyLost bool
	// presented maps the hosts the proxy presented a certificate to, to the
	// certificate's label.
	presented map[string]string
	// committed returns the recorded actions of an approved commit act at
	// key, which the act replays and asks no model about; nil when no act
	// is one.
	committed func(key string) ([]recordedAction, bool)
}

// holdsSecret reports whether value contains a value of secrets, however
// short: a value shown whole shows each secret in it.
func holdsSecret(value string, secrets ...map[string]string) bool {
	for _, values := range secrets {
		for _, secret := range values {
			if secret != "" && strings.Contains(value, secret) {
				return true
			}
		}
	}
	return false
}

// actRecorder keeps what acts and uploads did, keyed by replayKey and
// uploadReplayKey.
type actRecorder interface {
	// lookup returns what was recorded under key, if anything.
	lookup(key string) (recording, bool)
	stage(key string, rec recording)
	drop(key string)
}

// noRecording is why an act or an upload that may not call the model
// cannot run when there is nothing to replay.
const noRecording = "there is no recording of it on this host"

// replayMiss is the failure of an act or an upload that may not call the
// model: its recording is missing or no longer fits the page. The recording
// is kept for a run that lets the model repair it.
type replayMiss struct {
	kind   string
	reason string
}

func (m replayMiss) Error() string {
	return fmt.Sprintf("the %s cannot run without AI: %s", m.kind, m.reason)
}

// opResult is what one operation did.
type opResult struct {
	agentstep.Report
	// Actions are the actions an act or an upload performed or replayed.
	Actions []recordedAction
	// PageURL is the page an act or an upload started on, when it was
	// recorded.
	PageURL string
	// Values are the values an extract read, limited to the properties its
	// schema lists.
	Values map[string]any
}

func (o *operator) runOperation(ctx context.Context, index int, op operation) (opResult, error) {
	timeout := op.timeout()
	switch {
	case op.Goto != "":
		return o.gotoURL(ctx, index, op.Goto, timeout)
	case op.Act != nil:
		result, err := o.act(ctx, index, *op.Act, timeout)
		return result, o.explainWait(ctx, err)
	case op.Upload != nil:
		result, err := o.upload(ctx, index, *op.Upload, timeout)
		return result, o.explainWait(ctx, err)
	case op.Extract != nil:
		return o.extract(ctx, index, *op.Extract, timeout)
	case op.Expect != nil:
		return o.expect(ctx, index, *op.Expect, timeout)
	case op.Wait != nil:
		return o.wait(ctx, index, *op.Wait, timeout)
	case op.Screenshot != "":
		return o.screenshot(ctx, index, op.Screenshot)
	}
	return opResult{}, fmt.Errorf("unsupported operation %q", op.kind())
}

// checkPage fails when the page has left the allowed domains, which a
// redirect or an act can cause even when every goto target was allowed, or
// when a page the browser presents client certificates for did not load.
func (o *operator) checkPage(ctx context.Context, index int) error {
	if o.certificateProxy {
		if err := o.checkNavigation(ctx, index, ""); err != nil {
			return err
		}
	}
	if len(o.allowedDomains) == 0 {
		return nil
	}
	current, err := o.eng.CurrentURL(ctx)
	if err != nil {
		return err
	}
	if err := checkAllowedDomain(current, o.allowedDomains); err != nil {
		return fmt.Errorf("the page navigated away: %w", err)
	}
	return nil
}

func (o *operator) gotoURL(ctx context.Context, index int, target string, timeout time.Duration) (opResult, error) {
	began := time.Now()
	if err := checkAllowedDomain(target, o.allowedDomains); err != nil {
		return opResult{}, err
	}
	if err := o.eng.Goto(ctx, target, timeout); err != nil {
		return opResult{}, o.explainWait(ctx, err)
	}
	if err := o.checkNavigation(ctx, index, target); err != nil {
		return opResult{}, err
	}
	return opResult{Report: agentstep.Report{Index: index, Kind: opGoto, Subject: target, Status: agentstep.StatusCompleted, Duration: time.Since(began)}}, nil
}

// checkNavigation fails when a client certificate kept the page from
// opening: one the site asks for and no entry names, one it refused, or one
// the proxy could not present. target is the address a goto opened, if any.
// A page that cannot be read is left to the operations that follow.
func (o *operator) checkNavigation(ctx context.Context, index int, target string) error {
	failures := o.takeCertificateEvents(ctx, index)
	page, err := o.eng.PageOutcome(ctx)
	if err != nil {
		return nil
	}
	if page.Host == "" {
		page.Host = urlHost(target)
	}
	return certificateFailure(page, failures, o.presented, o.certificateProxy)
}

// pageRefusal returns the refusal the page shows, if it shows one. A page a
// client certificate kept from opening is the certificate's to explain, and
// a page that cannot be read shows none.
func (o *operator) pageRefusal(ctx context.Context) (refusal, bool) {
	page, err := o.eng.PageOutcome(ctx)
	if err != nil || certificateFailure(page, nil, o.presented, o.certificateProxy) != nil {
		return refusal{}, false
	}
	return refusalOf(page)
}

// explainWait adds to err the connections the certificate proxy still waits
// on, which tell where an operation that timed out got stuck.
func (o *operator) explainWait(ctx context.Context, err error) error {
	if err == nil || !o.certificateProxy || o.eng == nil {
		return err
	}
	pending, pendingErr := o.eng.PendingConnections(ctx)
	if pendingErr != nil {
		return err
	}
	var waits []string
	for _, conn := range pending {
		if waited := time.Since(conn.Since); waited >= pendingReportAfter {
			waits = append(waits, fmt.Sprintf("%s (%s for %s)", conn.Host, conn.Phase, waited.Round(time.Second)))
		}
	}
	if len(waits) == 0 {
		return err
	}
	return fmt.Errorf("%w; still waiting on %s", err, strings.Join(waits, ", "))
}

// takeCertificateEvents reports what the certificate proxy saw since the
// previous call, attributed to the operation at index, and returns its
// failures.
func (o *operator) takeCertificateEvents(ctx context.Context, index int) []certproxy.Event {
	if !o.certificateProxy || o.eng == nil {
		return nil
	}
	events, err := o.eng.TakeCertificateEvents(ctx)
	if err != nil && !o.certificateProxyLost {
		o.certificateProxyLost = true
		if o.warn != nil {
			o.warn(index, "the certificate proxy stopped answering: "+err.Error())
		}
	}
	var failures []certproxy.Event
	for _, event := range events {
		if o.certificateEvent != nil {
			o.certificateEvent(index, event)
		}
		switch {
		case event.Kind == certproxy.EventGuardOff:
			// It changes how the browser reaches sites, not why a page
			// failed.
		case event.Kind != certproxy.EventConnect:
			failures = append(failures, event)
		case event.Presented:
			if o.presented == nil {
				o.presented = map[string]string{}
			}
			o.presented[hostpattern.Canonical(event.Host)] = event.Entry
		}
	}
	return failures
}

// tlsVerified reports whether the certificate proxy verified the server
// certificate of the host of the https: page at address. Without an answer
// from the proxy, it reports false.
func (o *operator) tlsVerified(ctx context.Context, address string) bool {
	if !o.certificateProxy || o.certificateProxyLost || !strings.HasPrefix(strings.ToLower(address), "https://") {
		return false
	}
	verified, err := o.eng.CertificateVerified(ctx, urlHost(address))
	return err == nil && verified
}

func (o *operator) act(ctx context.Context, index int, spec actSpec, timeout time.Duration) (opResult, error) {
	// Validation guarantees every reference names a variable or an earlier
	// ask, so a missing value means that ask was skipped.
	for _, name := range agentstep.VariableReferences(spec.Instruction) {
		if _, ok := o.variables[name]; !ok {
			return opResult{}, fmt.Errorf("the instruction uses %%%s%%, but the ask that sets it did not run", name)
		}
	}
	began, before := time.Now(), o.bridge.totals()
	// The document the page shows before the act tells whether an act that
	// lost the page took effect. It stays empty when the page cannot be read.
	document, _ := o.eng.DocumentID(ctx)
	choice, recorded := o.decide(spec.AI, spec.Cache)
	key, pageURL := "", ""
	if recorded {
		var err error
		if pageURL, err = o.eng.CurrentURL(ctx); err != nil {
			return opResult{}, err
		}
		key = replayKey(index, spec.Instruction, pageURL)
	}
	status := agentstep.StatusCompleted
	rec, ok := o.recording(choice, key)
	actions := rec.Actions
	// An approved commit act does only what was approved: the actions it
	// recorded, never what a model picks.
	isCommit := false
	if o.committed != nil {
		var committed []recordedAction
		if committed, isCommit = o.committed(key); isCommit {
			choice, actions, ok = aiNever, committed, true
		}
	}
	// A recording made where the page already showed a value the act names
	// is not replayed with another value; it is kept for the runs it fits,
	// and what the model does here replaces it.
	if ok && !isCommit {
		if name := unfit(spec.Instruction, rec, o.variables); name != "" {
			reason := fmt.Sprintf("its recording does nothing with %%%s%%, so it fits only the value %%%s%% had when it was recorded", name, name)
			if choice == aiNever {
				return opResult{}, replayMiss{kind: opAct, reason: reason}
			}
			o.warn(index, "not replayed: "+reason)
			actions, ok = nil, false
		}
	}
	if !ok && choice == aiNever {
		return opResult{}, replayMiss{kind: opAct, reason: noRecording}
	}
	if ok {
		replayed, err := o.replay(ctx, actions, document, timeout)
		if err != nil {
			return opResult{}, err
		}
		if replayed == len(actions) {
			return opResult{
				Report: agentstep.Report{
					Index: index, Kind: opAct, Subject: spec.Instruction, Status: agentstep.StatusCacheHit, Via: agentstep.ViaElement,
					Detail: describeActions(actions), Duration: time.Since(began),
				},
				Actions: actions,
				PageURL: pageURL,
			}, nil
		}
		if choice == aiNever {
			return opResult{}, replayMiss{kind: opAct, reason: fmt.Sprintf(
				"recorded action %d of %d, %s, did not replay: its element is gone, hidden, or no longer the one recorded",
				replayed+1, len(actions), describeActions(actions[replayed:replayed+1]))}
		}
		status = agentstep.StatusHealed
		// The recording is dropped unless the act that heals it records what
		// it did.
		o.recorder.drop(key)
		// A recorded action before the miss may have loaded a new document.
		document, _ = o.eng.DocumentID(ctx)
	}
	outcome, err := o.performAct(ctx, index, spec.Instruction, document, timeout)
	if err != nil {
		return opResult{}, err
	}
	if !outcome.Success {
		if strings.Contains(outcome.Message, noActionFoundMessage) {
			return opResult{}, fmt.Errorf("the model (%s) answered that no element on the page matches the instruction; "+
				"if the element is on the page, reword the instruction or try another model, "+
				"since some models give this answer for every request", o.bridge.modelName())
		}
		return opResult{}, fmt.Errorf("act did not complete: %s", outcome.Message)
	}
	if recorded && len(outcome.Actions) > 0 {
		o.recorder.stage(key, recordAct(spec.Instruction, outcome.Actions, o.variables))
	}
	detail := describeActions(outcome.Actions)
	if len(outcome.Actions) == 0 {
		// An act counted done without its actions, because it loaded a new
		// document or closed its window, says so.
		detail = outcome.Message
	}
	return opResult{
		Report: agentstep.Report{
			Index: index, Kind: opAct, Subject: spec.Instruction, Status: status, Via: agentstep.ViaModel,
			Detail: detail, Tokens: o.bridge.totals().sub(before).total(),
			Duration: time.Since(began),
		},
		Actions: outcome.Actions,
		PageURL: pageURL,
	}, nil
}

// decide returns how much AI decides for an act or an upload with the given
// settings, and whether what it does is recorded. A step records under
// every choice, so a later switch to replaying starts with a recording.
func (o *operator) decide(ai string, cache *bool) (choice string, recorded bool) {
	switch {
	case o.recorder == nil:
		return aiOnMiss, false
	case o.choice == nil:
		return aiOnMiss, cache == nil || *cache
	default:
		return o.choice(ai, cache), true
	}
}

// recording returns what was recorded under key when the choice replays
// it. Under every_run nothing is looked up, so nothing counts as replayed.
func (o *operator) recording(choice, key string) (recording, bool) {
	if choice == aiEveryRun {
		return recording{}, false
	}
	return o.lookup(key)
}

// lookup returns what was recorded under key, if it holds any action.
func (o *operator) lookup(key string) (recording, bool) {
	if key == "" {
		return recording{}, false
	}
	rec, ok := o.recorder.lookup(key)
	return rec, ok && len(rec.Actions) > 0
}

// replay performs recorded actions in order and returns how many
// succeeded, stopping at the first that did not. document identifies the
// page's document before the first. An action that lost the page is judged
// by the document it started on: a new document means it took effect, and
// the next action runs there; the same document means it did not, and the
// replay ends so the model acts instead, unless the action lost its script
// context, as loadedNewDocument says.
func (o *operator) replay(ctx context.Context, actions []recordedAction, document string, timeout time.Duration) (int, error) {
	for i, action := range actions {
		if i > 0 {
			document, _ = o.eng.DocumentID(ctx)
		}
		replayed, err := o.eng.Replay(ctx, action, o.variables, timeout)
		if errors.Is(err, errPageSessionLost) {
			replayed, err = o.loadedNewDocument(ctx, document, err)
		}
		if err != nil || !replayed {
			return i, err
		}
	}
	return len(actions), nil
}

// performAct runs an act and judges one that lost the page by the page's
// document: a document other than document means the act took effect, and
// the same document means it did not, so it runs once more, unless the act
// lost its script context, as loadedNewDocument says. Acting again without
// that check could submit a form twice.
func (o *operator) performAct(ctx context.Context, index int, instruction, document string, timeout time.Duration) (actOutcome, error) {
	for retried := false; ; retried = true {
		outcome, err := o.eng.Act(ctx, instruction, o.variables, o.secret, timeout)
		if !errors.Is(err, errPageSessionLost) {
			return outcome, err
		}
		loaded, judgeErr := o.loadedNewDocument(ctx, document, err)
		if loaded {
			return actOutcome{Success: true, Message: "the page loaded a new document before the act reported back"}, nil
		}
		if judgeErr != nil {
			return actOutcome{}, judgeErr
		}
		if retried {
			return actOutcome{}, err
		}
		o.warn(index, "the browser lost its connection to the page before the act took effect; running it again")
	}
}

// loadedNewDocument reports whether the page shows a document other than
// document after lost, an error wrapping errPageSessionLost. It returns lost
// when the documents cannot be compared, and when lost wraps
// errPageContextLost but the page kept its document: a frame then loaded the
// document that replaced the context, which the action may have caused.
func (o *operator) loadedNewDocument(ctx context.Context, document string, lost error) (bool, error) {
	if document == "" {
		return false, lost
	}
	current, err := o.eng.DocumentID(ctx)
	if err != nil {
		return false, errors.Join(lost, err)
	}
	if current == document && errors.Is(lost, errPageContextLost) {
		return false, lost
	}
	return current != document, nil
}

func (o *operator) extract(ctx context.Context, index int, spec extractSpec, timeout time.Duration) (opResult, error) {
	began, before := time.Now(), o.bridge.totals()
	schema, err := json.Marshal(spec.Schema)
	if err != nil {
		return opResult{}, fmt.Errorf("encode extract schema: %w", err)
	}
	data, err := o.eng.Extract(ctx, spec.Instruction, schema, timeout)
	if err != nil {
		return opResult{}, err
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return opResult{}, fmt.Errorf("extract returned a non-object value: %w", err)
	}
	// Only the fields a schema lists are published, matching the output
	// names known when the DAG loads.
	properties, listed := spec.Schema["properties"].(map[string]any)
	published := make(map[string]any, len(values))
	for name, value := range values {
		if _, ok := properties[name]; listed && !ok {
			continue
		}
		published[name] = value
	}
	return opResult{
		Report: agentstep.Report{
			Index: index, Kind: opExtract, Subject: spec.Instruction, Status: agentstep.StatusCompleted, Via: agentstep.ViaModel,
			Detail: string(data), Tokens: o.bridge.totals().sub(before).total(), Duration: time.Since(began),
		},
		Values: published,
	}, nil
}

// expect fails unless the condition holds. A fixed check keeps reading the
// page until its within window or the operation timeout, because the page
// may still be updating.
func (o *operator) expect(ctx context.Context, index int, c condition, timeout time.Duration) (opResult, error) {
	began, before := time.Now(), o.bridge.totals()
	holds, reason, err := o.await(ctx, c, c.window(timeout), timeout)
	if err != nil {
		return opResult{}, err
	}
	if !holds {
		return opResult{}, fmt.Errorf("expectation not met: %s", reason)
	}
	return opResult{Report: agentstep.Report{
		Index: index, Kind: opExpect, Subject: c.String(), Status: agentstep.StatusCompleted, Via: c.via(), Detail: reason,
		Tokens: o.bridge.totals().sub(before).total(), Duration: time.Since(began),
	}}, nil
}

// await evaluates a condition. The model judges a statement once; a fixed
// check is read repeatedly until it holds or window passes. A zero window
// reads the page once. A read that lost its script context found the page
// between documents, which is not readable yet, so it fails the check only
// once window passes.
func (o *operator) await(ctx context.Context, c condition, window, timeout time.Duration) (bool, string, error) {
	deadline := time.Now().Add(window)
	for {
		holds, reason, err := o.evaluate(ctx, c, timeout)
		unreadable := errors.Is(err, errPageContextLost)
		if (err != nil && !unreadable) || holds || c.judged() || !time.Now().Before(deadline) {
			return holds, reason, err
		}
		select {
		case <-ctx.Done():
			return false, "", ctx.Err()
		case <-time.After(conditionPollInterval):
		}
	}
}

// evaluate reports whether a condition holds now, and why.
func (o *operator) evaluate(ctx context.Context, c condition, timeout time.Duration) (bool, string, error) {
	switch {
	case c.judged():
		return o.judge(ctx, c.Statement, timeout)
	case c.Text != "":
		text, err := o.eng.PageText(ctx)
		if err != nil {
			return false, "", err
		}
		if strings.Contains(text, c.Text) {
			return true, fmt.Sprintf("the page text contains %q", c.Text), nil
		}
		return false, fmt.Sprintf("the page text does not contain %q", c.Text), nil
	case c.Selector != "":
		visible, err := o.eng.SelectorVisible(ctx, c.Selector)
		if err != nil {
			return false, "", err
		}
		if visible {
			return true, fmt.Sprintf("%q is visible", c.Selector), nil
		}
		return false, fmt.Sprintf("%q is not visible", c.Selector), nil
	default:
		current, err := o.eng.CurrentURL(ctx)
		if err != nil {
			return false, "", err
		}
		if strings.Contains(current, c.URL) {
			return true, fmt.Sprintf("the URL contains %q", c.URL), nil
		}
		return false, fmt.Sprintf("the URL %s does not contain %q", current, c.URL), nil
	}
}

func (o *operator) wait(ctx context.Context, index int, spec waitSpec, timeout time.Duration) (opResult, error) {
	began := time.Now()
	subject := spec.Selector
	if spec.Duration != "" {
		duration, err := time.ParseDuration(spec.Duration)
		if err != nil || duration <= 0 {
			return opResult{}, fmt.Errorf("wait duration %q must be a positive duration", spec.Duration)
		}
		subject = spec.Duration
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return opResult{}, ctx.Err()
		case <-timer.C:
		}
	} else if err := o.eng.WaitForSelector(ctx, spec.Selector, timeout); err != nil {
		return opResult{}, err
	}
	return opResult{Report: agentstep.Report{Index: index, Kind: opWait, Subject: subject, Status: agentstep.StatusCompleted, Duration: time.Since(began)}}, nil
}

func (o *operator) screenshot(ctx context.Context, index int, name string) (opResult, error) {
	began := time.Now()
	data, err := o.eng.Screenshot(ctx)
	if err != nil {
		return opResult{}, err
	}
	rel, err := o.shots.WriteScreenshot(name, data)
	if err != nil {
		return opResult{}, err
	}
	return opResult{Report: agentstep.Report{
		Index: index, Kind: opScreenshot, Subject: name, Status: agentstep.StatusCompleted,
		Detail: rel, Duration: time.Since(began), Files: []string{rel},
	}}, nil
}

// judge asks the model whether a statement holds for the current page.
func (o *operator) judge(ctx context.Context, statement string, timeout time.Duration) (bool, string, error) {
	instruction := fmt.Sprintf("Decide whether this statement is true for the current page: %q. Set answer to true or false and give a one-sentence reason.", statement)
	data, err := o.eng.Extract(ctx, instruction, statementSchema, timeout)
	if err != nil {
		return false, "", err
	}
	var verdict struct {
		Answer bool   `json:"answer"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(data, &verdict); err != nil {
		return false, "", fmt.Errorf("decode statement verdict: %w", err)
	}
	return verdict.Answer, verdict.Reason, nil
}

// stepRecorder records a step's acts in its replay cache.
type stepRecorder struct {
	cache *replayCache
}

func (r stepRecorder) lookup(key string) (recording, bool) { return r.cache.Lookup(key) }
func (r stepRecorder) stage(key string, rec recording)     { r.cache.Stage(key, rec) }
func (r stepRecorder) drop(key string)                     { r.cache.Drop(key) }
