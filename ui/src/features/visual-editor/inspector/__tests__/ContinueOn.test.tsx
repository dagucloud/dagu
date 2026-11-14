// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { issuesSample } from '../../testing/fixtures';
import { ContinueOn, continueOnObject } from '../sections/flow/ContinueOn';
import { runStep } from './fixtures';
import { fieldOp, renderPart } from './flowHarness';

describe('continueOnObject', () => {
  it.each<[unknown, Record<string, unknown>]>([
    ['failed', { failure: true }],
    ['skipped', { skipped: true }],
    [' Failed ', { failure: true }],
    ['SKIPPED', { skipped: true }],
    [
      { failure: true, exit_code: [1] },
      { failure: true, exit_code: [1] },
    ],
    [undefined, {}],
    [['x'], {}],
  ])('%j gives %j', (value, expected) => {
    expect(continueOnObject(value)).toEqual(expected);
  });
});

describe('ContinueOn', () => {
  it('sets one key of the object form', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: { failure: true, exit_code: [1] } },
    });
    expect(
      screen.getByRole('switch', { name: 'Continue on failure' })
    ).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue when skipped' })
    );
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['continue_on', 'skipped'], true)
    );
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue on failure' })
    );
    expect(onEdit).toHaveBeenLastCalledWith(
      fieldOp(['continue_on', 'failure'], undefined)
    );
  });

  it('creates the key when the step has no continue_on', () => {
    const { onEdit } = renderPart(ContinueOn, runStep);
    fireEvent.click(screen.getByRole('switch', { name: 'Mark as success' }));
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['continue_on', 'mark_success'], true)
    );
  });

  it('replaces the shorthand with its object', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: 'failed' },
    });
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue when skipped' })
    );
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['continue_on'], { failure: true, skipped: true })
    );
  });

  it('reads the shorthand in any case', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: ' SKIPPED ' },
    });
    expect(
      screen.getByRole('switch', { name: 'Continue when skipped' })
    ).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue on failure' })
    );
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['continue_on'], { skipped: true, failure: true })
    );
  });

  // A step without continue_on takes defaults.continue_on, so the last
  // switch that goes off stays as an explicit false.
  it('keeps the last key as false when its switch goes off', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: { failure: true } },
    });
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue on failure' })
    );
    expect(onEdit.mock.calls).toEqual([
      [fieldOp(['continue_on', 'failure'], false)],
    ]);
  });

  it('keeps the shorthand as false when its switch goes off', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: 'skipped' },
    });
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue when skipped' })
    );
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['continue_on'], { skipped: false })
    );
  });

  // The server reads `failed` in the map as `failure`.
  it('reads the failed alias and removes it when the switch goes off', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: { failed: true } },
    });
    const failure = screen.getByRole('switch', { name: 'Continue on failure' });
    expect(failure).toHaveAttribute('aria-checked', 'true');
    fireEvent.click(failure);
    expect(onEdit.mock.calls).toEqual([
      [fieldOp(['continue_on', 'failure'], false)],
      [fieldOp(['continue_on', 'failed'], undefined)],
    ]);
  });

  it('replaces the failed alias when the switch goes on', () => {
    const { onEdit } = renderPart(ContinueOn, {
      ...runStep,
      value: { continue_on: { failed: false } },
    });
    fireEvent.click(
      screen.getByRole('switch', { name: 'Continue on failure' })
    );
    expect(onEdit.mock.calls).toEqual([
      [fieldOp(['continue_on', 'failure'], true)],
      [fieldOp(['continue_on', 'failed'], undefined)],
    ]);
  });

  it('shows the issues of a nested key', () => {
    renderPart(ContinueOn, runStep, {
      fieldIssues: { 'continue_on.exit_code': [issuesSample[0]!] },
    });
    expect(screen.getByRole('alert')).toHaveTextContent('unknown dependency');
  });

  it('emits nothing when read-only', () => {
    const { onEdit } = renderPart(ContinueOn, runStep, { readOnly: true });
    fireEvent.click(screen.getByRole('switch', { name: 'Mark as success' }));
    expect(onEdit).not.toHaveBeenCalled();
  });
});
