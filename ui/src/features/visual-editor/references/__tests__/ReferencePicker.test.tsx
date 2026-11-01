// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useRef } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { ReferenceCandidate } from '../../contracts';
import {
  ReferencePicker,
  type ReferencePickerHandle,
  type ReferencePickerProps,
} from '../ReferencePicker';

const candidates: ReferenceCandidate[] = [
  {
    token: '${params.API_URL}',
    label: 'API_URL',
    group: 'params',
    detail: 'https://api.example.com',
    known: 'static',
  },
  {
    token: '${steps.fetch.outputs.body}',
    label: 'fetch body',
    group: 'steps',
    stepName: 'fetch',
    distance: 1,
    known: 'runtime',
  },
  {
    token: '${env.HOME}',
    label: 'HOME',
    group: 'env',
    known: 'static',
  },
  {
    token: '${steps.fetch.outputs.status}',
    label: 'fetch status',
    group: 'steps',
    stepName: 'fetch',
    distance: 1,
    known: 'runtime',
  },
];

// The field stands for a host input that forwards its keys to the picker.
function Host(props: ReferencePickerProps) {
  const ref = useRef<ReferencePickerHandle>(null);
  return (
    <div>
      <input
        aria-label="keys"
        onKeyDown={(e) => {
          if (ref.current?.handleKey(e.key)) e.preventDefault();
        }}
      />
      <ReferencePicker ref={ref} {...props} />
    </div>
  );
}

function renderPicker(props: Partial<ReferencePickerProps> = {}) {
  const onPick = vi.fn();
  const onClose = vi.fn();
  render(
    <Host
      id="pk"
      candidates={candidates}
      onPick={onPick}
      onClose={onClose}
      {...props}
    />
  );
  return { onPick, onClose };
}

describe('ReferencePicker', () => {
  it('lists options grouped in the fixed group order', () => {
    renderPicker();
    const groups = screen.getAllByRole('group');
    expect(groups.map((g) => g.getAttribute('aria-labelledby'))).toEqual([
      'pk-group-steps',
      'pk-group-params',
      'pk-group-env',
    ]);
    expect(screen.getByText('Step outputs')).toBeInTheDocument();
    expect(screen.getByText('Parameters')).toBeInTheDocument();
    expect(screen.getByText('Environment variables')).toBeInTheDocument();
    const stepOptions = within(groups[0] as HTMLElement).getAllByRole('option');
    expect(stepOptions).toHaveLength(2);
    expect(stepOptions[0]).toHaveTextContent('${steps.fetch.outputs.body}');
    expect(screen.getByRole('option', { name: /API_URL/ })).toHaveTextContent(
      'https://api.example.com'
    );
  });

  it('marks runtime candidates as known after the run', () => {
    renderPicker();
    expect(screen.getAllByText('Known after run')).toHaveLength(2);
    expect(
      within(screen.getByRole('option', { name: /HOME/ })).queryByText(
        'Known after run'
      )
    ).toBeNull();
  });

  it('picks an option on click', () => {
    const { onPick } = renderPicker();
    fireEvent.click(screen.getByRole('option', { name: /HOME/ }));
    expect(onPick).toHaveBeenCalledWith(candidates[2]);
  });

  it('shows an empty state without candidates', () => {
    renderPicker({ candidates: [] });
    expect(screen.getByText('No references available')).toBeInTheDocument();
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('moves the active option with the arrow keys and wraps', () => {
    renderPicker();
    const keys = screen.getByLabelText('keys');
    const active = () =>
      screen
        .getAllByRole('option')
        .filter((o) => o.getAttribute('aria-selected') === 'true');
    expect(active()).toHaveLength(1);
    expect(active()[0]).toHaveTextContent('fetch body');
    fireEvent.keyDown(keys, { key: 'ArrowDown' });
    expect(active()[0]).toHaveTextContent('fetch status');
    fireEvent.keyDown(keys, { key: 'ArrowUp' });
    fireEvent.keyDown(keys, { key: 'ArrowUp' });
    expect(active()[0]).toHaveTextContent('HOME');
    fireEvent.keyDown(keys, { key: 'ArrowDown' });
    expect(active()[0]).toHaveTextContent('fetch body');
  });

  it('reports the active option id', () => {
    const onActiveChange = vi.fn();
    renderPicker({ onActiveChange });
    expect(onActiveChange).toHaveBeenLastCalledWith('pk-opt-0');
    fireEvent.keyDown(screen.getByLabelText('keys'), { key: 'ArrowDown' });
    expect(onActiveChange).toHaveBeenLastCalledWith('pk-opt-1');
    expect(screen.getAllByRole('option')[1]).toHaveAttribute('id', 'pk-opt-1');
  });

  it('picks the active option on Enter', () => {
    const { onPick } = renderPicker();
    const keys = screen.getByLabelText('keys');
    fireEvent.keyDown(keys, { key: 'ArrowDown' });
    fireEvent.keyDown(keys, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(candidates[3]);
  });

  it('closes on Escape', () => {
    const { onClose, onPick } = renderPicker();
    fireEvent.keyDown(screen.getByLabelText('keys'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onPick).not.toHaveBeenCalled();
  });

  it('leaves Enter and arrows alone when the list is empty', () => {
    const { onPick } = renderPicker({ candidates: [] });
    const keys = screen.getByLabelText('keys');
    expect(fireEvent.keyDown(keys, { key: 'Enter' })).toBe(true);
    expect(fireEvent.keyDown(keys, { key: 'ArrowDown' })).toBe(true);
    expect(onPick).not.toHaveBeenCalled();
  });

  it('filters the options with the search box and focuses it', () => {
    renderPicker({ showSearch: true });
    const search = screen.getByRole('textbox', { name: 'Search references' });
    expect(search).toHaveFocus();
    fireEvent.change(search, { target: { value: 'status' } });
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent('fetch status');
    expect(screen.queryByText('Parameters')).toBeNull();
  });

  it('navigates and picks from the search box', () => {
    const { onPick, onClose } = renderPicker({ showSearch: true });
    const search = screen.getByRole('textbox', { name: 'Search references' });
    fireEvent.keyDown(search, { key: 'ArrowDown' });
    expect(search).toHaveAttribute('aria-activedescendant', 'pk-opt-1');
    fireEvent.keyDown(search, { key: 'Enter' });
    expect(onPick).toHaveBeenCalledWith(candidates[3]);
    fireEvent.keyDown(search, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('shows an empty state when nothing matches', () => {
    renderPicker({ showSearch: true });
    fireEvent.change(
      screen.getByRole('textbox', { name: 'Search references' }),
      {
        target: { value: 'zzz' },
      }
    );
    expect(screen.getByText('No matching references')).toBeInTheDocument();
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('filters with the host query when there is no search box', () => {
    renderPicker({ query: 'params.api' });
    expect(screen.queryByLabelText('Search references')).toBeNull();
    const options = screen.getAllByRole('option');
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent('API_URL');
  });
});
