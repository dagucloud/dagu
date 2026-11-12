// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { parseTargets, RouteTable } from '../widgets';
import { routerStep } from './fixtures';

const routes = (routerStep.value.with as { routes: Record<string, string[]> })
  .routes;

describe('parseTargets', () => {
  it.each([
    ['a, b', ['a', 'b']],
    [' a ,, b ,', ['a', 'b']],
    ['', []],
  ])('%j gives %j', (text, expected) => {
    expect(parseTargets(text)).toEqual(expected);
  });
});

describe('RouteTable', () => {
  it('lists each pattern with its targets', () => {
    render(
      <RouteTable routes={routes} readOnly={false} onSetTargets={vi.fn()} />
    );
    // The pattern is a read-only field with the box of the targets.
    expect(screen.getByLabelText('Pattern 200')).toHaveValue('200');
    expect(screen.getByLabelText('Targets of re:^5')).toHaveValue(
      'retry, alert'
    );
  });

  it('sends new targets of a pattern on blur', () => {
    const onSetTargets = vi.fn();
    render(
      <RouteTable
        routes={routes}
        readOnly={false}
        onSetTargets={onSetTargets}
      />
    );
    const input = screen.getByLabelText('Targets of 200');
    fireEvent.change(input, { target: { value: 'ok, audit' } });
    fireEvent.blur(input);
    expect(onSetTargets).toHaveBeenCalledWith('200', ['ok', 'audit']);
  });

  it('sends nothing when the targets did not change', () => {
    const onSetTargets = vi.fn();
    render(
      <RouteTable
        routes={routes}
        readOnly={false}
        onSetTargets={onSetTargets}
      />
    );
    fireEvent.blur(screen.getByLabelText('Targets of 200'));
    expect(onSetTargets).not.toHaveBeenCalled();
  });

  // A step name can hold a comma: an untouched field keeps it one target.
  it('keeps a target with a comma when the field did not change', () => {
    const onSetTargets = vi.fn();
    render(
      <RouteTable
        routes={{ ok: ['Build, test'] }}
        readOnly={false}
        onSetTargets={onSetTargets}
      />
    );
    fireEvent.blur(screen.getByLabelText('Targets of ok'));
    expect(onSetTargets).not.toHaveBeenCalled();
  });

  it('sends targets that differ only in where the commas split them', () => {
    const onSetTargets = vi.fn();
    render(
      <RouteTable
        routes={{ ok: ['a,b'] }}
        readOnly={false}
        onSetTargets={onSetTargets}
      />
    );
    const input = screen.getByLabelText('Targets of ok');
    fireEvent.change(input, { target: { value: 'a, b' } });
    fireEvent.blur(input);
    expect(onSetTargets).toHaveBeenCalledWith('ok', ['a', 'b']);
  });

  it('removes a pattern with an empty target list', () => {
    const onSetTargets = vi.fn();
    render(
      <RouteTable
        routes={routes}
        readOnly={false}
        onSetTargets={onSetTargets}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Remove route 200' }));
    expect(onSetTargets).toHaveBeenCalledWith('200', []);
  });

  it('adds a pattern once it has a target', () => {
    const onSetTargets = vi.fn();
    render(
      <RouteTable
        routes={routes}
        readOnly={false}
        onSetTargets={onSetTargets}
      />
    );
    const add = screen.getByRole('button', { name: 'Add route' });
    fireEvent.change(screen.getByLabelText('New pattern'), {
      target: { value: 'num:>=400' },
    });
    expect(add).toBeDisabled();
    fireEvent.change(screen.getByLabelText('New targets'), {
      target: { value: 'alert' },
    });
    fireEvent.click(add);
    expect(onSetTargets).toHaveBeenCalledWith('num:>=400', ['alert']);
  });

  it('disables every input when read-only', () => {
    render(<RouteTable routes={routes} readOnly onSetTargets={vi.fn()} />);
    expect(screen.getByLabelText('Targets of 200')).toBeDisabled();
    expect(screen.queryByLabelText('New pattern')).toBeNull();
  });
});
