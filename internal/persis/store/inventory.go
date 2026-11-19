// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ inventory.Store = (*InventoryStore)(nil)

// InventoryStore implements [inventory.Store] over a [persis.Collection], one
// record per inventory keyed by its ID. Every change and every read that spans
// inventories holds one lock, so a check and the write that follows it never
// interleave with another change, and no read sees an import partly applied.
type InventoryStore struct {
	col  persis.Collection
	lock persis.Lock
}

// inventoryStoredRecord keeps its own timestamps, since a backend may derive a
// record's from the time it was last written.
type inventoryStoredRecord struct {
	// SchemaVersion is the record's format. Version counts the changes stored.
	SchemaVersion int                 `json:"schema_version"`
	Version       int64               `json:"version"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	Inventory     inventory.Inventory `json:"inventory"`
}

// storedInventory pairs a decoded inventory with the record it came from, so
// a change can swap exactly the bytes it checked.
type storedInventory struct {
	rec    *persis.Record
	stored inventory.Stored
}

const (
	// inventorySchemaVersion is the record format this version writes. A
	// newer format is refused on read, so a record is never written back
	// without the fields this version does not know.
	inventorySchemaVersion = 1

	inventoryIDPrefix = "inv_"
	// inventoryIDLength is the number of random characters after the prefix.
	inventoryIDLength = 10
	// createAttempts bounds retries after a generated ID collides.
	createAttempts = 3
)

// inventoryIDPattern accepts the IDs the store generates. A record ID is a
// file name, so any other ID names no inventory.
var inventoryIDPattern = regexp.MustCompile(`^inv_[a-z2-7]+$`)

var errIDSet = errors.New("is set by Dagu and cannot be given")

// NewInventoryStore creates an InventoryStore. lock serializes changes across
// every process that shares col.
func NewInventoryStore(col persis.Collection, lock persis.Lock) (*InventoryStore, error) {
	if col == nil {
		return nil, errors.New("inventory store: collection cannot be nil")
	}
	if lock == nil {
		return nil, errors.New("inventory store: lock cannot be nil")
	}
	return &InventoryStore{col: col, lock: lock}, nil
}

func (s *InventoryStore) List(ctx context.Context) ([]inventory.Stored, error) {
	all, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]inventory.Stored, len(all))
	for i, e := range all {
		out[i] = e.stored
	}
	slices.SortFunc(out, func(a, b inventory.Stored) int {
		return cmp.Or(
			strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			strings.Compare(a.ID, b.ID),
		)
	})
	return out, nil
}

func (s *InventoryStore) Get(ctx context.Context, id string) (inventory.Stored, error) {
	e, err := s.get(ctx, id)
	if err != nil {
		return inventory.Stored{}, err
	}
	return e.stored, nil
}

func (s *InventoryStore) Create(ctx context.Context, inv inventory.Inventory) (inventory.Stored, error) {
	if inv.ID != "" {
		return inventory.Stored{}, &inventory.Error{Inventory: inv.Name, Field: "id", Err: errIDSet}
	}
	var out inventory.Stored
	err := s.lock.Do(ctx, func(held func() error) error {
		all, err := s.load(ctx)
		if err != nil {
			return err
		}
		if _, err := inventory.Resolve(append(inventoriesOf(all), inv)); err != nil {
			return err
		}
		if err := held(); err != nil {
			return err
		}
		out, err = s.create(ctx, inv)
		return err
	})
	return out, err
}

func (s *InventoryStore) Replace(ctx context.Context, inv inventory.Inventory, version int64) (inventory.Stored, error) {
	var out inventory.Stored
	err := s.lock.Do(ctx, func(held func() error) error {
		all, err := s.load(ctx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(all, func(e storedInventory) bool { return e.stored.ID == inv.ID })
		if i < 0 {
			return inventory.ErrNotFound
		}
		current := all[i]
		if current.stored.Version != version {
			return inventory.ErrStale
		}
		// The changed inventory goes last, so a clash is reported on it and
		// names the inventory that already holds the name.
		others := inventoriesOf(slices.Delete(all, i, i+1))
		if _, err := inventory.Resolve(append(others, inv)); err != nil {
			return err
		}
		if err := held(); err != nil {
			return err
		}
		out, err = s.replace(ctx, current, inv)
		return err
	})
	return out, err
}

func (s *InventoryStore) Delete(ctx context.Context, id string, version int64) error {
	return s.lock.Do(ctx, func(held func() error) error {
		current, err := s.get(ctx, id)
		if err != nil {
			return err
		}
		if current.stored.Version != version {
			return inventory.ErrStale
		}
		if err := held(); err != nil {
			return err
		}
		err = s.col.CompareAndDelete(ctx, current.rec)
		switch {
		case errors.Is(err, persis.ErrNotFound):
			return inventory.ErrNotFound
		case errors.Is(err, persis.ErrConflict):
			return inventory.ErrStale
		case err != nil:
			return fmt.Errorf("inventory store: delete %q: %w", id, err)
		}
		return nil
	})
}

func (s *InventoryStore) Import(ctx context.Context, invs []inventory.Inventory, opts inventory.ImportOptions) (inventory.ImportResult, error) {
	var result inventory.ImportResult
	err := s.lock.Do(ctx, func(held func() error) error {
		all, err := s.load(ctx)
		if err != nil {
			return err
		}
		plan, err := planImport(all, invs, opts.Replace)
		if err != nil {
			return err
		}
		if opts.DryRun {
			result = plan.result()
			return nil
		}
		// Each inventory is stored on its own. When this stops partway, the
		// ones stored so far stay, and the same import with Replace finishes.
		for _, step := range plan.steps {
			if err := held(); err != nil {
				return err
			}
			if step.current == nil {
				_, err = s.create(ctx, step.inv)
			} else {
				_, err = s.replace(ctx, *step.current, step.inv)
			}
			if err != nil {
				return err
			}
		}
		result = plan.result()
		return nil
	})
	return result, err
}

func (s *InventoryStore) Nodes(ctx context.Context) ([]inventory.ResolvedNode, error) {
	all, err := s.loadLocked(ctx)
	if err != nil {
		return nil, err
	}
	return inventory.Resolve(inventoriesOf(all))
}

func (s *InventoryStore) Select(ctx context.Context, selector string) ([]inventory.ResolvedNode, error) {
	filters, err := inventory.ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	return inventory.Select(nodes, filters), nil
}

// loadLocked reads every inventory under the lock. An import writes one
// inventory at a time, so a read between two of its writes could miss a node
// that moves between inventories, and a step would then run on part of the
// fleet without an error. A read during which the lock passed to another
// client may have seen such a change, so it fails.
func (s *InventoryStore) loadLocked(ctx context.Context) ([]storedInventory, error) {
	var all []storedInventory
	err := s.lock.Do(ctx, func(held func() error) error {
		loaded, err := s.load(ctx)
		if err != nil {
			return err
		}
		if err := held(); err != nil {
			return err
		}
		all = loaded
		return nil
	})
	return all, err
}

// load reads every inventory. A record that cannot be read fails the load,
// since a check that skipped it could let a change break a rule.
func (s *InventoryStore) load(ctx context.Context) ([]storedInventory, error) {
	recs, err := listAllStrict(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, fmt.Errorf("inventory store: list: %w", err)
	}
	out := make([]storedInventory, 0, len(recs))
	for _, rec := range recs {
		e, err := decodeStoredInventory(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *InventoryStore) get(ctx context.Context, id string) (storedInventory, error) {
	if !inventoryIDPattern.MatchString(id) {
		return storedInventory{}, inventory.ErrNotFound
	}
	rec, err := s.col.Get(ctx, id)
	if errors.Is(err, persis.ErrNotFound) {
		return storedInventory{}, inventory.ErrNotFound
	}
	if err != nil {
		return storedInventory{}, fmt.Errorf("inventory store: get %q: %w", id, err)
	}
	return decodeStoredInventory(rec)
}

func (s *InventoryStore) create(ctx context.Context, inv inventory.Inventory) (inventory.Stored, error) {
	now := time.Now().UTC()
	for range createAttempts {
		inv.ID = newInventoryID()
		data, err := persis.Encode(&inventoryStoredRecord{
			SchemaVersion: inventorySchemaVersion,
			Version:       1,
			CreatedAt:     now,
			UpdatedAt:     now,
			Inventory:     inv,
		})
		if err != nil {
			return inventory.Stored{}, fmt.Errorf("inventory store: encode %q: %w", inv.Name, err)
		}
		err = s.col.Create(ctx, &persis.Record{ID: inv.ID, Data: data, CreatedAt: now, UpdatedAt: now})
		if errors.Is(err, persis.ErrConflict) {
			continue
		}
		if err != nil {
			return inventory.Stored{}, fmt.Errorf("inventory store: create %q: %w", inv.Name, err)
		}
		return inventory.Stored{Inventory: inv, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
	}
	return inventory.Stored{}, fmt.Errorf("inventory store: create %q: no free ID after %d attempts", inv.Name, createAttempts)
}

func (s *InventoryStore) replace(ctx context.Context, current storedInventory, inv inventory.Inventory) (inventory.Stored, error) {
	inv.ID = current.stored.ID
	next := inventoryStoredRecord{
		SchemaVersion: inventorySchemaVersion,
		Version:       current.stored.Version + 1,
		CreatedAt:     current.stored.CreatedAt,
		UpdatedAt:     time.Now().UTC(),
		Inventory:     inv,
	}
	data, err := persis.Encode(&next)
	if err != nil {
		return inventory.Stored{}, fmt.Errorf("inventory store: encode %q: %w", inv.Name, err)
	}
	err = s.col.CompareAndSwap(ctx, inv.ID, current.rec.Data, data)
	switch {
	case errors.Is(err, persis.ErrNotFound):
		return inventory.Stored{}, inventory.ErrNotFound
	case errors.Is(err, persis.ErrConflict):
		return inventory.Stored{}, inventory.ErrStale
	case err != nil:
		return inventory.Stored{}, fmt.Errorf("inventory store: replace %q: %w", inv.ID, err)
	}
	return storedOf(next), nil
}

// importPlan is a checked import: the inventories to create, and those that
// replace a stored one.
type importPlan struct {
	steps []importStep
}

type importStep struct {
	inv inventory.Inventory
	// current is the stored inventory this one replaces, or nil to create it.
	current *storedInventory
}

func planImport(all []storedInventory, invs []inventory.Inventory, replace bool) (importPlan, error) {
	var (
		plan     importPlan
		errs     []error
		replaced = make([]bool, len(all))
	)
	for _, inv := range invs {
		if inv.ID != "" {
			errs = append(errs, &inventory.Error{Inventory: inv.Name, Field: "id", Err: errIDSet})
		}
		i := slices.IndexFunc(all, func(e storedInventory) bool { return strings.EqualFold(e.stored.Name, inv.Name) })
		switch {
		case i < 0:
			plan.steps = append(plan.steps, importStep{inv: inv})
		case !replace:
			errs = append(errs, &inventory.Error{Inventory: inv.Name, Field: "name", Err: inventory.ErrExists})
		default:
			replaced[i] = true
			plan.steps = append(plan.steps, importStep{inv: inv, current: &all[i]})
		}
	}
	// Imported inventories go last, so a clash is reported on them and names
	// the stored inventory that keeps the name.
	set := make([]inventory.Inventory, 0, len(all)+len(invs))
	for i, e := range all {
		if !replaced[i] {
			set = append(set, e.stored.Inventory)
		}
	}
	set = append(set, invs...)
	if _, err := inventory.Resolve(set); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return importPlan{}, errors.Join(errs...)
	}
	return plan, nil
}

func (p importPlan) result() inventory.ImportResult {
	var r inventory.ImportResult
	for _, step := range p.steps {
		if step.current == nil {
			r.Created = append(r.Created, step.inv.Name)
		} else {
			r.Replaced = append(r.Replaced, step.inv.Name)
		}
	}
	return r
}

func decodeStoredInventory(rec *persis.Record) (storedInventory, error) {
	var payload inventoryStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return storedInventory{}, fmt.Errorf("inventory store: decode %q: %w", rec.ID, err)
	}
	if payload.SchemaVersion > inventorySchemaVersion {
		return storedInventory{}, fmt.Errorf("inventory store: %q has schema version %d, newer than %d, which this version of Dagu reads",
			rec.ID, payload.SchemaVersion, inventorySchemaVersion)
	}
	payload.Inventory.ID = rec.ID
	return storedInventory{rec: rec, stored: storedOf(payload)}, nil
}

func storedOf(r inventoryStoredRecord) inventory.Stored {
	return inventory.Stored{
		Inventory: r.Inventory,
		Version:   r.Version,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func inventoriesOf(all []storedInventory) []inventory.Inventory {
	out := make([]inventory.Inventory, len(all))
	for i, e := range all {
		out[i] = e.stored.Inventory
	}
	return out
}

// newInventoryID returns a random ID such as inv_k3x7m2qa9z. The characters
// are lowercase, so IDs stay distinct as file names on file systems that
// ignore case.
func newInventoryID() string {
	return inventoryIDPrefix + strings.ToLower(rand.Text()[:inventoryIDLength])
}
