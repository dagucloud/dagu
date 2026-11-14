// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { issuesSample } from '../../testing/fixtures';
import { Dependencies } from '../sections/flow/Dependencies';
import { numberOrText, Timing } from '../sections/flow/Timing';
import { runStep } from './fixtures';
import { fieldOp, renderPart } from './flowHarness';

describe('Dependencies', () => {
  it('lists the dependencies read-only with the canvas hint', () => {
    renderPart(Dependencies, runStep, {
      fieldIssues: { depends: [issuesSample[0]!] },
    });
    expect(screen.getByRole('list', { name: 'Depends on' })).toHaveTextContent(
      'fetchextract'
    );
    expect(
      screen.getByText('Edit dependencies on the canvas')
    ).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('unknown dependency');
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('shows the issues of one dependency', () => {
    renderPart(Dependencies, runStep, {
      fieldIssues: { 'depends[1]': [issuesSample[0]!] },
    });
    expect(screen.getByRole('alert')).toHaveTextContent('unknown dependency');
  });

  it('tells when there is no dependency', () => {
    renderPart(Dependencies, { ...runStep, depends: [] });
    expect(screen.getByText('No dependencies')).toBeInTheDocument();
  });
});

describe('numberOrText', () => {
  it.each([
    ['', undefined],
    [' 30 ', 30],
    ['${TIMEOUT}', '${TIMEOUT}'],
    ['1.5', '1.5'],
  ])('%j gives %j', (text, expected) => {
    expect(numberOrText(text)).toBe(expected);
  });
});

describe('Timing', () => {
  it('edits the timeout', () => {
    const { onEdit, edit } = renderPart(Timing, runStep);
    expect(screen.getByLabelText('Timeout (seconds)')).toHaveValue('60');
    edit('Timeout (seconds)', '90');
    expect(onEdit).toHaveBeenCalledWith(fieldOp(['timeout_sec'], 90));
    edit('Timeout (seconds)', '');
    expect(onEdit).toHaveBeenLastCalledWith(
      fieldOp(['timeout_sec'], undefined)
    );
  });

  it('edits one key of the retry policy', () => {
    const { onEdit, edit } = renderPart(Timing, {
      ...runStep,
      value: { retry_policy: { limit: 2, interval_sec: 30 } },
    });
    edit('Retry limit', '${RETRIES}');
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith(
      fieldOp(['retry_policy', 'limit'], '${RETRIES}')
    );
  });

  it('shows a field issue next to the timeout', () => {
    renderPart(Timing, runStep, {
      fieldIssues: { timeout_sec: [issuesSample[0]!] },
    });
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  it('emits nothing when read-only', () => {
    const { onEdit, edit } = renderPart(Timing, runStep, { readOnly: true });
    edit('Timeout (seconds)', '5');
    expect(onEdit).not.toHaveBeenCalled();
  });
});
