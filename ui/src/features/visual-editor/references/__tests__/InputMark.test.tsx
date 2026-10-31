// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { InputProvenance } from '../../run/inputProvenance';
import { InputMark, inputMarkTitle } from '../InputMark';

const T0 = Date.parse('2026-10-01T00:00:00Z');
const record = (extra: Partial<InputProvenance> = {}): InputProvenance => ({
  step: 'slow',
  source: 'run',
  runId: '034mroF1udXVyvPz00bn5a',
  stale: false,
  capturedAt: T0,
  ...extra,
});
const ts = (key: string, values?: Record<string, string | number>) =>
  key.replace(/\{(\w+)\}/g, (_, name: string) => String(values?.[name] ?? ''));

describe('inputMarkTitle', () => {
  it('names the run with its short id and the time, for a run source', () => {
    expect(inputMarkTitle(record(), ts)).toMatch(
      /^Outputs reused from run 034mroF1, 2026-10-01/
    );
  });

  it('names the run of cached values', () => {
    expect(inputMarkTitle(record({ source: 'cache' }), ts)).toMatch(
      /^Cached values from run 034mroF1, 2026-10-01/
    );
    expect(
      inputMarkTitle(record({ source: 'cache', capturedAt: null }), ts)
    ).toBe('Cached values from run 034mroF1');
  });

  it('says that the cache of an older run has no run', () => {
    expect(
      inputMarkTitle(record({ source: 'cache', runId: undefined }), ts)
    ).toMatch(/^From your cache \(2026-10-01/);
  });

  it('adds that the step changed since', () => {
    expect(inputMarkTitle(record({ stale: true }), ts)).toContain(
      'The step changed since'
    );
  });
});

describe('InputMark', () => {
  it('writes "from run <id>" for a run and "cached from run <id>, <time>" for the cache', () => {
    const { rerender } = render(<InputMark provenance={record()} />);
    const mark = screen.getByTestId('input-mark');
    expect(mark).toHaveTextContent('from run 034mroF1');
    expect(mark).toHaveAttribute('data-source', 'run');
    rerender(<InputMark provenance={record({ source: 'cache' })} />);
    expect(screen.getByTestId('input-mark')).toHaveTextContent(
      /cached from run 034mroF1/
    );
    expect(screen.getByTestId('input-mark')).toHaveAttribute(
      'data-source',
      'cache'
    );
  });

  it('makes the run a button that selects it when the host gives a callback', () => {
    const onSelectRun = vi.fn();
    render(<InputMark provenance={record()} onSelectRun={onSelectRun} />);
    fireEvent.click(screen.getByTestId('input-mark-run'));
    expect(onSelectRun).toHaveBeenCalledWith('034mroF1udXVyvPz00bn5a');
  });

  it('says that a stale source changed since, in the warning tone', () => {
    render(<InputMark provenance={record({ stale: true })} />);
    const mark = screen.getByTestId('input-mark');
    expect(mark).toHaveTextContent('changed since run 034mroF1');
    expect(mark).toHaveAttribute('data-stale');
  });

  it('shows a chip with a dashed outline for the card of the graph', () => {
    render(<InputMark provenance={record()} variant="chip" />);
    const chip = screen.getByTestId('input-mark');
    expect(chip.className).toContain('border-dashed');
    expect(screen.getByTestId('input-mark-chip-text')).toHaveTextContent(
      'from run 034mroF1'
    );
    expect(chip.getAttribute('aria-label')).toContain(
      '(034mroF1udXVyvPz00bn5a)'
    );
  });

  it('says "cache of run" on the chip of cached values', () => {
    render(
      <InputMark provenance={record({ source: 'cache' })} variant="chip" />
    );
    expect(screen.getByTestId('input-mark-chip-text')).toHaveTextContent(
      'cache of run 034mroF1'
    );
  });

  it('shows the icon alone for a compact card', () => {
    render(<InputMark provenance={record()} variant="icon" />);
    expect(screen.queryByTestId('input-mark-chip-text')).toBeNull();
    expect(
      screen.getByTestId('input-mark').querySelector('svg')
    ).not.toBeNull();
  });
});
