// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import {
  cacheOptionText,
  RunSourcePicker,
  blockedText,
} from '../RunSourcePicker';
import type { SourceOption } from '../runSourceOptions';

const ts = (key: string, values?: Record<string, string | number>) =>
  key.replace(/\{(\w+)\}/g, (_, name: string) => String(values?.[name] ?? ''));

const options: SourceOption[] = [
  {
    id: 'run:RunAAAAAAAA1',
    kind: 'run',
    runId: 'RunAAAAAAAA1',
    status: Status.Success,
    startedAt: '2026-10-01T00:00:00Z',
    finishedAt: '2026-10-01T00:00:09Z',
    auto: true,
  },
  {
    id: 'run:RunBBBBBBBB2',
    kind: 'run',
    runId: 'RunBBBBBBBB2',
    status: Status.Success,
    startedAt: '2026-10-01T00:10:00Z',
    finishedAt: '2026-10-01T00:10:02Z',
    ephemeral: true,
  },
  {
    id: 'run:RunCCCCCCCC3',
    kind: 'run',
    runId: 'RunCCCCCCCC3',
    status: Status.Failed,
    startedAt: '2026-09-30T00:00:00Z',
    finishedAt: '2026-09-30T00:00:01Z',
    blocked: { reason: 'missing', steps: ['slow', 'middle'] },
  },
  {
    id: 'cache',
    kind: 'cache',
    cache: {
      runIds: ['RunAAAAAAAA1'],
      capturedAt: Date.parse('2026-10-01T00:01:00Z'),
    },
  },
];

async function open(selectedId: string | null, onSelect = vi.fn()) {
  render(
    <RunSourcePicker
      options={options}
      selectedId={selectedId}
      onSelect={onSelect}
    />
  );
  await userEvent.click(screen.getByTestId('run-source-picker'));
  return { onSelect, menu: await screen.findByTestId('run-source-menu') };
}

describe('RunSourcePicker', () => {
  it('shows the run in use on the trigger, with its short id', () => {
    render(
      <RunSourcePicker
        options={options}
        selectedId="run:RunAAAAAAAA1"
        onSelect={vi.fn()}
      />
    );
    expect(screen.getByTestId('run-source-picker')).toHaveTextContent(
      'RunAAAAA'
    );
  });

  it('says the cached values when the cache is in use, and no run with none', () => {
    const { rerender } = render(
      <RunSourcePicker
        options={options}
        selectedId="cache"
        onSelect={vi.fn()}
      />
    );
    expect(screen.getByTestId('run-source-picker')).toHaveTextContent(
      'Cached values'
    );
    rerender(
      <RunSourcePicker options={options} selectedId={null} onSelect={vi.fn()} />
    );
    expect(screen.getByTestId('run-source-picker')).toHaveTextContent('No run');
  });

  it('is absent when there is nothing to choose', () => {
    render(
      <RunSourcePicker options={[]} selectedId={null} onSelect={vi.fn()} />
    );
    expect(screen.queryByTestId('run-source-picker')).toBeNull();
  });

  it('lists the runs, the automatic one first, and the cached values last', async () => {
    const { menu } = await open('run:RunAAAAAAAA1');
    const items = within(menu).getAllByRole('menuitemradio');
    expect(items).toHaveLength(4);
    expect(items[0]).toHaveAttribute('aria-checked', 'true');
    expect(items[1]).toHaveAttribute('aria-checked', 'false');
    expect(items[0]).toHaveTextContent('RunAAAAA');
    expect(items[0]).toHaveTextContent('Automatic');
    expect(items[3]).toHaveTextContent('Cached values (from run RunAAAAA');
  });

  it('flags a test run that still exists', async () => {
    const { menu } = await open(null);
    const item = within(menu).getByTestId('run-source-option-run:RunBBBBBBBB2');
    expect(within(item).getByTestId('run-source-test')).toHaveAccessibleName(
      'Test run'
    );
  });

  it('disables a run that misses outputs, with the reason in view', async () => {
    const { menu, onSelect } = await open(null);
    const item = within(menu).getByTestId('run-source-option-run:RunCCCCCCCC3');
    expect(item).toHaveAttribute('data-disabled');
    expect(within(item).getByTestId('run-source-reason')).toHaveTextContent(
      'missing: slow, middle'
    );
    await userEvent.click(item);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('gives the chosen option', async () => {
    const { menu, onSelect } = await open('run:RunAAAAAAAA1');
    await userEvent.click(
      within(menu).getByTestId('run-source-option-run:RunBBBBBBBB2')
    );
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect.mock.calls[0]?.[0]).toMatchObject({
      runId: 'RunBBBBBBBB2',
    });
  });
});

describe('the texts of the options', () => {
  it('names the run and the time of the cached values, or the runs', () => {
    const one = cacheOptionText(options[3]!, ts);
    expect(one).toContain('from run RunAAAAA');
    const many = cacheOptionText(
      {
        id: 'cache',
        kind: 'cache',
        cache: { runIds: ['RunAAAAAAAA1', 'RunBBBBBBBB2'], capturedAt: 0 },
      },
      ts
    );
    expect(many).toContain('from runs RunAAAAA, RunBBBBB');
  });

  it('gives the reason that an option is blocked', () => {
    expect(blockedText(options[2]!, ts)).toBe('missing: slow, middle');
    expect(
      blockedText({ id: 'x', kind: 'run', blocked: { reason: 'active' } }, ts)
    ).toBe('still running');
    expect(blockedText(options[0]!, ts)).toBeNull();
  });
});
