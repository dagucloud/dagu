// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { StepView } from '../../contracts';
import { RunEditor } from '../sections/RunEditor';
import { runStep } from './fixtures';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));

const oneLine: StepView = {
  ...runStep,
  value: { name: 'build', run: 'make build' },
};

function setup(step: StepView, readOnly = false) {
  const onEdit = vi.fn();
  render(
    <RunEditor
      step={step}
      readOnly={readOnly}
      onEdit={onEdit}
      fieldIssues={{}}
    />
  );
  return onEdit;
}

describe('RunEditor layout', () => {
  // One look for the shell select and the text inputs; the Multi-line toggle
  // sits on the row of the field label.
  it('styles the shell select like an input and aligns the toggle', () => {
    setup(oneLine);
    const select = screen.getByRole('combobox');
    expect(select).toHaveClass('h-7', 'w-full', 'rounded-md', 'bg-card');
    const toggle = screen.getByRole('button', { name: 'Multi-line' });
    const row = toggle.parentElement!;
    expect(row).toHaveTextContent('Command');
    expect(row).toHaveClass('justify-between');
  });
});

describe('RunEditor', () => {
  it('edits a one-line command', () => {
    const onEdit = setup(oneLine);
    const input = screen.getByRole('textbox', { name: 'Command' });
    fireEvent.change(input, { target: { value: 'make test' } });
    fireEvent.blur(input);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['run'],
      value: 'make test',
    });
  });

  // A step written with the classic `command:` key keeps that key.
  it('shows and edits the command of a step that uses `command`', () => {
    const onEdit = setup({
      ...oneLine,
      value: { name: 'slow', command: 'sleep 90' },
    });
    const input = screen.getByRole('textbox', { name: 'Command' });
    expect(input).toHaveValue('sleep 90');
    fireEvent.change(input, { target: { value: 'sleep 1' } });
    fireEvent.blur(input);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['command'],
      value: 'sleep 1',
    });
  });

  // A legacy step with `script:` only keeps that key.
  it('shows and edits the script of a step that uses `script`', () => {
    const onEdit = setup({
      ...oneLine,
      value: { name: 'greet', script: 'echo hello' },
    });
    const input = screen.getByRole('textbox', { name: 'Command' });
    expect(input).toHaveValue('echo hello');
    fireEvent.change(input, { target: { value: 'echo bye' } });
    fireEvent.blur(input);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['script'],
      value: 'echo bye',
    });
  });

  it('shows a list under `command` read-only', () => {
    setup({ ...oneLine, value: { command: ['make a', 'make b'] } });
    expect(screen.getByText('make b')).toBeInTheDocument();
  });

  it('edits a multi-line script in the code editor', async () => {
    const onEdit = setup(runStep);
    const editor = await screen.findByTestId('monaco');
    expect(editor).toHaveAttribute('data-language', 'shell');
    expect(editor).toHaveValue('make build\nmake test\n');
    fireEvent.change(editor, { target: { value: 'make build\nmake lint\n' } });
    expect(onEdit).not.toHaveBeenCalled();
    fireEvent.blur(editor);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['run'],
      value: 'make build\nmake lint\n',
    });
  });

  it('sends nothing when the script did not change', async () => {
    const onEdit = setup(runStep);
    fireEvent.blur(await screen.findByTestId('monaco'));
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('switches a one-line command to the code editor', async () => {
    setup(oneLine);
    const toggle = screen.getByRole('button', { name: 'Multi-line' });
    expect(toggle).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(toggle);
    expect(await screen.findByTestId('monaco')).toHaveValue('make build');
  });

  // The server refuses a top-level `shell` next to `run`.
  it('sets and removes the shell in with', () => {
    const onEdit = setup(oneLine);
    const select = screen.getByRole('combobox');
    fireEvent.change(select, { target: { value: 'bash' } });
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['with', 'shell'],
      value: 'bash',
    });
    fireEvent.change(select, { target: { value: '' } });
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['with', 'shell'],
      value: undefined,
    });
  });

  it('sets the top-level shell of a step that uses `command`', () => {
    const onEdit = setup({
      ...oneLine,
      value: { command: 'sleep 90', shell: 'sh' },
    });
    const select = screen.getByRole('combobox');
    expect(select).toHaveValue('sh');
    fireEvent.change(select, { target: { value: 'bash' } });
    expect(onEdit).toHaveBeenLastCalledWith({
      kind: 'setStepField',
      step: 'steps[2]',
      path: ['shell'],
      value: 'bash',
    });
  });

  it('offers a custom shell of the step', () => {
    setup({ ...oneLine, value: { run: 'x', with: { shell: 'fish' } } });
    expect(screen.getByRole('combobox')).toHaveValue('fish');
  });

  it('shows a list of commands read-only', () => {
    const onEdit = setup({
      ...oneLine,
      value: { run: ['make build', 'make test'] },
    });
    expect(screen.getByText('make test')).toBeInTheDocument();
    expect(screen.getByText('Edit this field in YAML')).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('sends nothing when read-only', () => {
    const onEdit = setup(oneLine, true);
    const input = screen.getByRole('textbox', { name: 'Command' });
    expect(input).toBeDisabled();
    expect(screen.getByRole('combobox')).toBeDisabled();
    fireEvent.blur(input);
    expect(onEdit).not.toHaveBeenCalled();
  });
});
