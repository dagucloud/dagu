// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ADD_STEP_ALIASES } from '../addStepAliases';
import { AddStepPanel, buildSections } from '../AddStepPanel';
import { rankCatalog } from '../canvas/paletteModel';
import type { ActionCatalogEntry } from '../contracts';
import { buildActionCatalog } from '../inspector/catalog';
import { realSchema } from '../inspector/__tests__/realSchema';

// The aliases must name actions of the real catalog: a missing schema is a
// failure here, not a skip.
function realCatalog(): ActionCatalogEntry[] {
  expect(realSchema).not.toBeNull();
  return buildActionCatalog(realSchema!, []);
}

const TITLES = { recent: 'Recently used', popular: 'Popular' };

function renderPanel(catalog: ActionCatalogEntry[]) {
  const onPick = vi.fn();
  render(
    <AddStepPanel
      catalog={catalog}
      targetLabel="At the end of the workflow"
      recent={[]}
      onPick={onPick}
      onClose={vi.fn()}
    />
  );
  const search = screen.getByRole('combobox', { name: 'Search actions' });
  return { onPick, search };
}

beforeEach(() => localStorage.clear());

// parity:action-catalog
describe('the aliases of the add-step search', () => {
  it('has the common words that users type', () => {
    for (const word of ['curl', 'sleep', 'email', 'sql', 'if', 'subworkflow']) {
      expect(ADD_STEP_ALIASES[word]?.length ?? 0).toBeGreaterThan(0);
    }
  });

  it('names only actions that are in the catalog', () => {
    const names = new Set(realCatalog().map((entry) => entry.name));
    const missing = Object.entries(ADD_STEP_ALIASES).flatMap(
      ([alias, targets]) =>
        targets
          .filter((target) => !names.has(target))
          .map((target) => `${alias} -> ${target}`)
    );
    expect(missing).toEqual([]);
  });

  it('puts the action of an alias first', () => {
    const catalog = realCatalog();
    const first = (query: string) =>
      rankCatalog(catalog, query, ADD_STEP_ALIASES)[0]?.name;
    expect(first('curl')).toBe('http.request');
    expect(first('sleep')).toBe('wait.duration');
    expect(first('email')).toBe('mail.send');
    expect(first('subworkflow')).toBe('dag.run');
    expect(first('if')).toBe('router.route');
  });

  it('finds every target of an alias', () => {
    const catalog = realCatalog();
    for (const [alias, targets] of Object.entries(ADD_STEP_ALIASES)) {
      const found = rankCatalog(catalog, alias, ADD_STEP_ALIASES).map(
        (entry) => entry.name
      );
      expect(found).toEqual(expect.arrayContaining([...targets]));
    }
  });
});

// parity:add-step-panel
describe('the ranked search of the panel', () => {
  const make = (
    name: string,
    namespace: string,
    title: string,
    description?: string
  ): ActionCatalogEntry => ({
    name,
    namespace,
    title,
    description,
    source: 'builtin',
    template: { action: name },
  });
  const catalog = [
    make('echo.print', 'echo', 'Print', 'Print a line to the log.'),
    make('audit.append', 'audit', 'Append a log entry'),
    make('log.write', 'log', 'Write a line'),
  ];

  it('shows the group of the best match first', () => {
    expect(buildSections(catalog, 'log', [], TITLES).map((s) => s.id)).toEqual([
      'log',
      'audit',
      'echo',
    ]);
  });

  it('adds the best match with Enter, not the first of the catalog', () => {
    const { onPick, search } = renderPanel(catalog);
    fireEvent.change(search, { target: { value: 'log' } });

    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(3);
    expect(options[0]).toHaveTextContent('log.write');
    expect(options[1]).toHaveTextContent('audit.append');
    expect(options[2]).toHaveTextContent('echo.print');
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(catalog[2]);
  });

  it('finds an action by an alias in the real catalog', () => {
    const { onPick, search } = renderPanel(realCatalog());
    fireEvent.change(search, { target: { value: 'curl' } });

    expect(screen.getAllByRole('option')[0]).toHaveTextContent('http.request');
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'http.request' })
    );
  });
});
