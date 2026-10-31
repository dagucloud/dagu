// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { StepView } from '../../contracts';
import { catalogSample } from '../../testing/fixtures';
import { ActionSection, planActionChange } from '../sections/Action';
import { httpStep, runStep } from './fixtures';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));

const [runEntry, httpEntry, jqEntry] = catalogSample as [
  (typeof catalogSample)[0],
  (typeof catalogSample)[0],
  (typeof catalogSample)[0],
];
const set = (step: string, path: string[], value: unknown) => ({
  kind: 'setStepField',
  step,
  path,
  value,
});
const bare: StepView = {
  ...httpStep,
  value: { name: 'fetch', action: 'http.request' },
};

describe('planActionChange', () => {
  it('gives nothing for the current entry', () => {
    expect(planActionChange(httpStep, httpEntry)).toBeNull();
    expect(planActionChange(runStep, runEntry)).toBeNull();
  });

  it('changes an action and asks before it removes with', () => {
    expect(planActionChange(httpStep, jqEntry)).toEqual({
      confirm: true,
      ops: [
        set('steps[0]', ['action'], 'jq.filter'),
        set('steps[0]', ['with'], undefined),
        set('steps[0]', ['with'], { filter: '.', data: '' }),
      ],
      keepOps: [set('steps[0]', ['action'], 'jq.filter')],
    });
  });

  it('changes an action without with at once', () => {
    expect(planActionChange(bare, jqEntry)).toEqual({
      confirm: false,
      ops: [
        set('steps[0]', ['action'], 'jq.filter'),
        set('steps[0]', ['with'], { filter: '.', data: '' }),
      ],
    });
  });

  it('turns a command step into an action', () => {
    expect(
      planActionChange(
        { ...runStep, value: { run: 'x', shell: 'bash' } },
        httpEntry
      )
    ).toEqual({
      confirm: false,
      ops: [
        set('steps[2]', ['run'], undefined),
        set('steps[2]', ['shell'], undefined),
        set('steps[2]', ['action'], 'http.request'),
        set('steps[2]', ['with'], { method: 'GET', url: '' }),
      ],
    });
  });

  // The `with` of a `run:` step holds its shell settings only.
  it('asks before it replaces the with of a command step', () => {
    expect(
      planActionChange(
        { ...runStep, value: { run: 'x', with: { shell: 'bash' } } },
        httpEntry
      )
    ).toEqual({
      confirm: true,
      ops: [
        set('steps[2]', ['run'], undefined),
        set('steps[2]', ['action'], 'http.request'),
        set('steps[2]', ['with'], undefined),
        set('steps[2]', ['with'], { method: 'GET', url: '' }),
      ],
    });
  });

  it('removes a classic command key when a step becomes an action', () => {
    expect(
      planActionChange({ ...runStep, value: { command: 'x' } }, httpEntry)
    ).toEqual({
      confirm: false,
      ops: [
        set('steps[2]', ['command'], undefined),
        set('steps[2]', ['action'], 'http.request'),
        set('steps[2]', ['with'], { method: 'GET', url: '' }),
      ],
    });
  });

  it('turns an action into a command', () => {
    expect(planActionChange(httpStep, runEntry)).toEqual({
      confirm: true,
      ops: [
        set('steps[0]', ['action'], undefined),
        set('steps[0]', ['with'], undefined),
        set('steps[0]', ['run'], ''),
      ],
    });
    expect(planActionChange(bare, runEntry)?.confirm).toBe(false);
  });
});

function setup(step: StepView, readOnly = false) {
  const onEdit = vi.fn();
  render(
    <ActionSection
      step={step}
      readOnly={readOnly}
      onEdit={onEdit}
      fieldIssues={{}}
      catalog={catalogSample}
    />
  );
  const pick = (name: RegExp) => {
    fireEvent.click(screen.getByRole('button', { name: 'Change the action' }));
    fireEvent.click(screen.getByRole('option', { name }));
  };
  return { onEdit, pick };
}

describe('ActionSection', () => {
  it('removes the parameters after the confirm', () => {
    const { onEdit, pick } = setup(httpStep);
    pick(/jq filter/);
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    expect(onEdit.mock.calls.map((call) => call[0].path)).toEqual([
      ['action'],
      ['with'],
      ['with'],
    ]);
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  it('keeps the parameters on request', () => {
    const { onEdit, pick } = setup(httpStep);
    pick(/jq filter/);
    fireEvent.click(screen.getByRole('button', { name: 'Keep' }));
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith(
      set('steps[0]', ['action'], 'jq.filter')
    );
  });

  it('cancels the change to a command on Keep', () => {
    const { onEdit, pick } = setup(httpStep);
    pick(/Run command/);
    fireEvent.click(screen.getByRole('button', { name: 'Keep' }));
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('shows the command editor for a command step', async () => {
    setup(runStep);
    expect(
      screen.getByRole('button', { name: 'Change the action' })
    ).toHaveTextContent('Run command');
    expect(await screen.findByTestId('monaco')).toBeInTheDocument();
  });

  it('emits nothing for the current entry', () => {
    const { onEdit, pick } = setup(httpStep);
    pick(/HTTP request/);
    expect(onEdit).not.toHaveBeenCalled();
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  it('leaves a sub-DAG call to the YAML section', () => {
    const { onEdit } = setup({
      ...httpStep,
      kind: 'call',
      action: undefined,
      value: { name: 'child', call: 'child-dag' },
    });
    expect(
      screen.getByText('Edit the kind of this step in the YAML section.')
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Change the action' })
    ).toBeNull();
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('disables the picker when read-only', () => {
    setup(httpStep, true);
    expect(
      screen.getByRole('button', { name: 'Change the action' })
    ).toBeDisabled();
  });
});
