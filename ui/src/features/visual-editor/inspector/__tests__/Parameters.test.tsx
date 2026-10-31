// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type { StepView, ValidationIssue } from '../../contracts';
import {
  FORM_CONTROLS,
  PARAMETERS_DEBOUNCE_MS,
  ParametersSection,
} from '../sections/Parameters';
import {
  httpStep,
  httpStepWithHeaders,
  routerStep,
  schemaSample,
} from './fixtures';

function setup(
  step: StepView,
  options: {
    readOnly?: boolean;
    fieldIssues?: Record<string, ValidationIssue[]>;
  } = {}
) {
  const onEdit = vi.fn();
  const view = render(
    <ParametersSection
      step={step}
      readOnly={options.readOnly ?? false}
      onEdit={onEdit}
      fieldIssues={options.fieldIssues ?? {}}
      schema={schemaSample}
      customActions={[]}
      candidates={[]}
    />
  );
  return { ...view, onEdit };
}

const wait = () =>
  act(() => vi.advanceTimersByTime(PARAMETERS_DEBOUNCE_MS + 10));

describe('ParametersSection', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  // The widgets of the theme set their own heights: the form container pins
  // one height for text, number and select controls.
  it('pins one control height and a gap under group descriptions', () => {
    setup(httpStep);
    const box = document.querySelector('[data-field-path="with"]')!;
    expect(box.className).toContain(FORM_CONTROLS);
    expect(FORM_CONTROLS).toContain(':h-7');
    expect(FORM_CONTROLS).toContain('select-trigger');
    expect(FORM_CONTROLS).toContain('__description');
  });

  it('emits one setStepField for one changed field', () => {
    const { onEdit } = setup(httpStep);
    const url = screen.getByLabelText('url');
    expect(url).toHaveValue('${params.API_URL}');
    fireEvent.change(url, { target: { value: 'https://example.com/items' } });
    expect(onEdit).not.toHaveBeenCalled();
    wait();
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'url'],
      value: 'https://example.com/items',
    });
  });

  it('coalesces several changes of one field', () => {
    const { onEdit } = setup(httpStep);
    const url = screen.getByLabelText('url');
    fireEvent.change(url, { target: { value: 'h' } });
    fireEvent.change(url, { target: { value: 'ht' } });
    wait();
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit.mock.calls[0]?.[0]?.value).toBe('ht');
  });

  it('removes a header with an undefined value', () => {
    const { onEdit } = setup(httpStepWithHeaders);
    const headers = screen.getByRole('group', { name: 'headers' });
    const remove = headers.querySelectorAll('button[aria-label="Remove"]');
    fireEvent.click(remove[0]!);
    wait();
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[0]',
      path: ['with', 'headers', 'X'],
      value: undefined,
    });
  });

  it('emits nothing on mount and unmount', () => {
    const { onEdit, unmount } = setup(httpStepWithHeaders);
    wait();
    unmount();
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('sends a pending change on unmount', () => {
    const { onEdit, unmount } = setup(httpStep);
    fireEvent.change(screen.getByLabelText('url'), { target: { value: 'u' } });
    unmount();
    expect(onEdit).toHaveBeenCalledTimes(1);
  });

  it('emits nothing when read-only', () => {
    const { onEdit, unmount } = setup(httpStep, { readOnly: true });
    expect(screen.getByLabelText('url')).toBeDisabled();
    fireEvent.change(screen.getByLabelText('url'), { target: { value: 'u' } });
    wait();
    unmount();
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('lists the keys that the schema does not know and keeps them', () => {
    const { onEdit } = setup(httpStepWithHeaders);
    const other = screen.getByTestId('ve-other-keys');
    expect(other).toHaveTextContent('legacy_key');
    expect(other).toHaveTextContent('"kept"');
    fireEvent.change(screen.getByLabelText('url'), { target: { value: 'u' } });
    wait();
    expect(onEdit.mock.calls.map((call) => call[0].path)).toEqual([
      ['with', 'url'],
    ]);
  });

  it('shows a field issue next to its field', () => {
    const issue: ValidationIssue = {
      severity: ValidationIssueSeverity.error,
      source: ValidationIssueSource.build,
      message: 'url must use https',
      fieldPath: 'steps[0].with.url',
    };
    setup(httpStep, { fieldIssues: { 'with.url': [issue] } });
    const message = screen.getByText('url must use https');
    const url = screen.getByLabelText('url');
    const timeout = document.querySelector('input[type="number"]')!;
    // The message sits between the url input and the next field.
    const after = Node.DOCUMENT_POSITION_FOLLOWING;
    expect(url.compareDocumentPosition(message) & after).toBeTruthy();
    expect(message.compareDocumentPosition(timeout) & after).toBeTruthy();
  });

  it('shows the essential fields of a large action', () => {
    setup({
      ...httpStep,
      action: 'k8s.run',
      value: {
        action: 'k8s.run',
        with: { image: 'alpine', pod: { restart_policy: 'Never' } },
      },
    });
    expect(
      screen.getByText('Edit the other fields in YAML')
    ).toBeInTheDocument();
    expect(screen.getByLabelText('image')).toHaveValue('alpine');
    expect(screen.queryByText('pod')).toBeNull();
    expect(screen.queryByTestId('ve-other-keys')).toBeNull();
  });

  it('edits the routes of a router step', () => {
    const { onEdit } = setup(routerStep);
    expect(screen.getByLabelText('value')).toHaveValue('${CODE}');
    fireEvent.click(screen.getByRole('button', { name: 'Remove route 200' }));
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setRouteTargets',
      step: 'steps[3]',
      pattern: '200',
      targets: [],
    });
    expect(screen.queryByTestId('ve-other-keys')).toBeNull();
  });

  it('explains when the action has no parameter form', () => {
    setup({
      ...httpStep,
      action: 'noop',
      value: { action: 'noop', with: { a: 1 } },
    });
    expect(
      screen.getByText(
        'No form for the parameters of this action. Edit them in the YAML section.'
      )
    ).toBeInTheDocument();
    expect(screen.getByTestId('ve-other-keys')).toHaveTextContent('a');
  });
});
