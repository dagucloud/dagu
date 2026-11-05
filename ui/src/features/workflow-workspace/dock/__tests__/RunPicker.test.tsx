// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { run } from '../../data/__tests__/fixtures';
import { RunPicker } from '../RunPicker';

// Oldest first, as the timeline draws them.
const RUNS = [
  run('older-run-1', undefined, Status.Failed),
  run('newer-run-2', undefined, Status.Success),
];
const statusOf = (r: { status: Status }) =>
  r.status === Status.Failed ? 'Failed' : 'Succeeded';

// The timeline of a phone: one menu of the runs in place of the bars.
describe('RunPicker', () => {
  it('names the selected run and lists the runs newest first', async () => {
    const onSelect = vi.fn();
    render(
      <RunPicker
        runs={RUNS}
        selectedId="newer-run-2"
        statusOf={statusOf}
        onSelect={onSelect}
      />
    );
    const picker = screen.getByTestId('ww-run-picker');
    expect(picker).toHaveTextContent('Succeeded');
    expect(picker).toHaveTextContent('newer-ru');

    await userEvent.click(picker);
    const items = await screen.findAllByRole('menuitemradio');
    expect(items.map((item) => item.textContent)).toEqual([
      expect.stringContaining('newer-ru'),
      expect.stringContaining('older-ru'),
    ]);
    expect(items[0]).toHaveAttribute('aria-checked', 'true');
    await userEvent.click(items[1]!);
    expect(onSelect).toHaveBeenCalledWith('older-run-1');
  });

  it('is disabled with no run', () => {
    render(
      <RunPicker
        runs={[]}
        selectedId={null}
        statusOf={statusOf}
        onSelect={vi.fn()}
      />
    );
    expect(screen.getByTestId('ww-run-picker')).toBeDisabled();
    expect(screen.getByTestId('ww-run-picker')).toHaveTextContent('No run');
  });
});
