// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

// sharedBrowsers holds the browsers that the browser steps of one DAG run
// share by browser.share name. A step uses a shared browser from its start
// to its end, so the steps sharing one take turns; between them the
// browser stays open on the page the last step left. The run closes them
// when it ends.
type sharedBrowsers struct {
	mu       sync.Mutex
	browsers map[string]*sharedBrowser
	// profiles holds, by name, the profiles a step of the run holds or
	// waits to lock.
	profiles map[string]*profileGate
	closed   bool
}

// profileGate is a profile a step of the run holds or waits to lock. share
// names the shared browser it is for, which keeps it until the run ends,
// and is empty for a step's own browser, which frees it when the step
// ends. freed closes once the profile is free.
type profileGate struct {
	share string
	freed chan struct{}
}

// sharedBrowser is the browser shared under one browser.share name.
type sharedBrowser struct {
	name string
	// turn holds a token while no step uses the browser.
	turn chan struct{}

	mu sync.Mutex
	// holder is the step using the browser, or "" when none does.
	holder string
	// open is set while the browser runs, whether a step uses it or not.
	// starter is the step that started it, and settings the launch
	// settings it started with.
	open     bool
	starter  string
	settings string
	// parked keeps the browser while it is open and no step uses it.
	parked *parkedBrowser
}

// parkedBrowser is what keeps a shared browser open between the steps that
// use it: its record, which finds it again and closes it should the run's
// process end without closing it, and the lease on its profile.
type parkedBrowser struct {
	record browserhost.Record
	store  *browserhost.Store
	lease  *profileLease
}

type sharedBrowsersKey struct{}

// WithSharedBrowsers returns a context under which the browser steps that
// give the same browser.share name use one browser, and a function that
// closes the browsers they left open. A DAG run calls it once and closes
// them when it ends.
func WithSharedBrowsers(ctx context.Context) (context.Context, func() error) {
	shared := &sharedBrowsers{browsers: map[string]*sharedBrowser{}, profiles: map[string]*profileGate{}}
	return context.WithValue(ctx, sharedBrowsersKey{}, shared), shared.close
}

func sharedBrowsersFrom(ctx context.Context) *sharedBrowsers {
	shared, _ := ctx.Value(sharedBrowsersKey{}).(*sharedBrowsers)
	return shared
}

// take waits until no other step uses the browser shared as name and gives
// it to step. waiting runs, with the step using it, when step has to wait.
func (s *sharedBrowsers) take(ctx context.Context, name, step string, waiting func(holder string)) (*sharedBrowser, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("browser.share %q: the run has closed its shared browsers", name)
	}
	b := s.browsers[name]
	if b == nil {
		b = &sharedBrowser{name: name, turn: make(chan struct{}, 1)}
		b.turn <- struct{}{}
		s.browsers[name] = b
	}
	s.mu.Unlock()
	select {
	case <-b.turn:
	default:
		waiting(b.currentHolder())
		select {
		case <-b.turn:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for the browser shared as %q: %w", name, ctx.Err())
		}
	}
	b.mu.Lock()
	b.holder = step
	b.mu.Unlock()
	return b, nil
}

// acquireProfile takes the named profile for a step of the run whose
// browser is shared as share, or is its own when share is empty. It waits
// while another step of the run holds the profile for a browser of its
// own, and fails at once when a browser the run shares holds it, since
// that browser keeps it until the run ends. It waits for other runs as
// acquireProfile does.
func (s *sharedBrowsers) acquireProfile(ctx context.Context, browserDir, name, ownRecordID, share string) (*profileLease, error) {
	gate := &profileGate{share: share, freed: make(chan struct{})}
	for {
		s.mu.Lock()
		held := s.profiles[name]
		if held == nil {
			s.profiles[name] = gate
		}
		s.mu.Unlock()
		if held == nil {
			break
		}
		if held.share != "" {
			return nil, &profileHeldError{message: fmt.Sprintf(
				"browser profile %q is held by the browser shared as %q in this run, which keeps it until the run ends; give this step browser.share: %s to use that browser",
				name, held.share, held.share)}
		}
		select {
		case <-held.freed:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for browser profile %q: %w", name, ctx.Err())
		}
	}
	lease, err := acquireProfile(ctx, browserDir, name, ownRecordID, true)
	if err != nil {
		s.freeProfile(name, gate)
		return nil, err
	}
	lease.onRelease = func() { s.freeProfile(name, gate) }
	return lease, nil
}

func (s *sharedBrowsers) freeProfile(name string, gate *profileGate) {
	s.mu.Lock()
	if s.profiles[name] == gate {
		delete(s.profiles, name)
	}
	s.mu.Unlock()
	close(gate.freed)
}

// openBrowsers returns the names of the shared browsers that are open.
func (s *sharedBrowsers) openBrowsers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for name, b := range s.browsers {
		b.mu.Lock()
		if b.open {
			names = append(names, name)
		}
		b.mu.Unlock()
	}
	slices.Sort(names)
	return names
}

// close closes the shared browsers no step uses, as the run does when it
// ends.
func (s *sharedBrowsers) close() error {
	s.mu.Lock()
	s.closed = true
	browsers := slices.Collect(maps.Values(s.browsers))
	s.mu.Unlock()
	var errs []error
	for _, b := range browsers {
		parked := b.claim()
		if parked == nil {
			continue
		}
		b.closedBrowser()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		errs = append(errs, browserhost.Release(ctx, parked.store, parked.record))
		cancel()
		parked.lease.release()
	}
	return errors.Join(errs...)
}

func (b *sharedBrowser) currentHolder() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.holder
}

// started returns the step that started the open browser and the launch
// settings it started with.
func (b *sharedBrowser) started() (starter, settings string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starter, b.settings
}

// adopt makes the browser of the step holding the turn the shared browser,
// unless another is open, and reports whether it did.
func (b *sharedBrowser) adopt(step, settings string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open {
		return false
	}
	b.open, b.starter, b.settings = true, step, settings
	return true
}

// claim hands the step holding the turn what keeps the open browser, or
// nil when none is parked.
func (b *sharedBrowser) claim() *parkedBrowser {
	b.mu.Lock()
	defer b.mu.Unlock()
	parked := b.parked
	b.parked = nil
	return parked
}

// park keeps the open browser until the next step claims it.
func (b *sharedBrowser) park(parked *parkedBrowser) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.parked = parked
}

// closedBrowser records that the shared browser is no longer open, so the
// next step starts a new one.
func (b *sharedBrowser) closedBrowser() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.open, b.starter, b.settings, b.parked = false, "", "", nil
}

// release ends the turn of the step using the browser.
func (b *sharedBrowser) release() {
	b.mu.Lock()
	b.holder = ""
	b.mu.Unlock()
	b.turn <- struct{}{}
}

// takeShared waits for the step's turn at the browser it shares, if any.
func (r *run) takeShared(ctx context.Context) error {
	name := r.cfg.Browser.Share
	if name == "" {
		return nil
	}
	shared := sharedBrowsersFrom(ctx)
	if shared == nil {
		r.warn(-1, fmt.Sprintf("browser.share %q has no effect outside a DAG run, so the step uses a browser of its own", name))
		return nil
	}
	b, err := shared.take(ctx, name, r.stepName, func(holder string) {
		r.timeline.Lifecycle(agentstep.StatusRunning, fmt.Sprintf("Waiting for step %q to finish with the browser shared as %q", holder, name))
	})
	if err != nil {
		return err
	}
	r.shared = b
	return nil
}

// joinShared reattaches to the shared browser a step before left open and
// reports whether it did. A browser that no longer answers is closed, so
// the step starts a new one.
func (r *run) joinShared(ctx context.Context, generation int) (bool, error) {
	if r.shared == nil {
		return false, nil
	}
	parked := r.shared.claim()
	if parked == nil {
		return false, nil
	}
	if starter, settings := r.shared.started(); settings != r.cfg.launchSettings() {
		r.shared.park(parked)
		return false, fmt.Errorf("browser.share %q: step %q started the shared browser with other browser settings; "+
			"give every step that shares it the same headless, executable, viewport, proxy, profile, client_certificates, and write_gate",
			r.shared.name, starter)
	}
	downloads, err := downloadsDir(r.artifacts)
	if err != nil {
		r.shared.park(parked)
		return false, err
	}
	opts := launchOptions{
		DownloadsDir:   downloads,
		AllowedDomains: r.cfg.Browser.AllowedDomains,
		DismissConfirm: r.cfg.dismissesConfirm(),
		Generate:       r.bridge.generate,
	}
	eng, err := r.exec.launcher.Reattach(ctx, recordedHandle(parked.record), opts)
	if err != nil {
		r.warn(-1, fmt.Sprintf("the browser shared as %q no longer answers, so a new one starts: %s", r.shared.name, r.masker.MaskString(err.Error())))
		r.shared.closedBrowser()
		releaseErr := browserhost.Release(ctx, parked.store, parked.record)
		parked.lease.release()
		if releaseErr != nil {
			return false, fmt.Errorf("close the browser shared as %q: %w", r.shared.name, releaseErr)
		}
		return false, nil
	}
	r.timeline.Lifecycle(agentstep.StatusRunning, fmt.Sprintf("Using the browser shared as %q", r.shared.name))
	r.eng = eng
	r.sharedOwn = true
	r.profile = parked.lease
	r.record = parked.record
	r.record.StepName = r.stepName
	r.record.Generation = generation
	r.record.DownloadsDir = downloads
	r.certificateProxy = r.record.CertProxyURL != ""
	r.writeSeq = r.record.WriteGateSeq
	if r.record.WriteGateURL != "" {
		if r.writeGate, err = writeGateMode(ctx, r.cfg.Browser.WriteGate); err != nil {
			return true, err
		}
		r.loadCommitPoints()
	}
	return true, r.saveRunningRecord()
}

// adoptShared makes the browser the step started, or resumed after an ask,
// the browser it shares, unless another step started one meanwhile.
func (r *run) adoptShared() {
	if r.shared != nil && r.eng != nil {
		r.sharedOwn = r.shared.adopt(r.stepName, r.cfg.launchSettings())
	}
}

// keepShared parks the browser the step shares, unless it can no longer
// be trusted, and reports whether it did. A browser it does not park
// closes with the step, so the next step sharing it starts a new one.
func (r *run) keepShared(ctx context.Context) bool {
	if !r.sharedOwn {
		return false
	}
	r.sharedOwn = false
	if r.eng != nil && !r.dropShared {
		err := r.parkShared(ctx)
		if err == nil {
			return true
		}
		r.warn(-1, "keep the shared browser open: "+r.masker.MaskString(err.Error()))
	}
	r.shared.closedBrowser()
	return false
}

// parkShared leaves the step's browser open, on the page it shows, for the
// next step that shares it.
func (r *run) parkShared(ctx context.Context) error {
	if err := r.endWrites(ctx); err != nil {
		return err
	}
	if err := r.eng.Detach(ctx); err != nil {
		return err
	}
	r.eng = nil
	record := r.record
	record.ID = browserhost.SharedRecordID(r.dagRunID, r.shared.name)
	record.State = browserhost.StateRunning
	record.Deadline = time.Time{}
	record.Cursor = 0
	record.Outputs, record.Downloads = nil, nil
	record.ReplayPending, record.ReplayUsed = nil, nil
	record.WriteGateSeq = r.writeSeq
	// The run's process keeps the browser, so a sweep closes it should the
	// process end without closing it.
	record.OwnerPID = os.Getpid()
	record.OwnerStartedAt, _ = procutil.StartTime(record.OwnerPID)
	if err := r.store.Save(record); err != nil {
		return err
	}
	if record.ID != r.record.ID {
		// A step resumed after an ask kept the browser under its own record.
		_ = r.store.Delete(r.record.ID)
	}
	r.shared.park(&parkedBrowser{record: record, store: r.store, lease: r.profile})
	r.profile = nil
	r.record = browserhost.Record{}
	return nil
}

// detachShared takes the browser out of the run's shared browsers before
// it waits for an ask's answer, apart from the steps that share it, under
// the step's own record, where the resumed step finds it.
func (r *run) detachShared() {
	if r.shared == nil {
		return
	}
	if r.sharedOwn {
		r.sharedOwn = false
		r.shared.closedBrowser()
	}
	if r.record.ID != r.stepRecordID {
		_ = r.store.Delete(r.record.ID)
		r.record.ID = r.stepRecordID
		r.record.StepName = r.stepName
	}
}

// leaveShared ends the step's turn at the browser it shares.
func (r *run) leaveShared() {
	if r.shared != nil {
		r.shared.release()
		r.shared = nil
	}
}

// lockProfile takes the named profile for the step, through its run's
// shared browsers when it runs in one, so it never waits for a profile its
// own run keeps until it ends.
func (r *run) lockProfile(ctx context.Context, name, recordID string) (*profileLease, error) {
	shared := sharedBrowsersFrom(ctx)
	if shared == nil {
		return acquireProfile(ctx, r.browser, name, recordID, true)
	}
	share := ""
	if r.shared != nil {
		share = r.shared.name
	}
	return shared.acquireProfile(ctx, r.browser, name, recordID, share)
}

// checkSharedSlots fails when the browsers the run shares take every place
// browser.max_open allows: they stay open until the run ends, so a launch
// waiting for a place would wait for its own run.
func checkSharedSlots(ctx context.Context) error {
	limit := cmnconfig.GetConfig(ctx).Browser.MaxOpen
	shared := sharedBrowsersFrom(ctx)
	if limit < 1 || shared == nil {
		return nil
	}
	open := shared.openBrowsers()
	if len(open) < limit {
		return nil
	}
	return fmt.Errorf("browser.max_open allows %d open browsers, and this run keeps %d open until it ends for its shared browsers %s; "+
		"raise browser.max_open, or give this step the browser.share of one of them",
		limit, len(open), strings.Join(open, ", "))
}
