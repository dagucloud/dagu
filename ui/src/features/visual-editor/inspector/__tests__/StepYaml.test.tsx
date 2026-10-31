// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StepYamlSection } from '../sections/StepYaml';
import { httpStep, modelHttpJq, schemaSample } from './fixtures';

vi.mock(
  '@monaco-editor/react',
  async () => (await import('./monacoMock')).monacoModule
);
vi.mock('../monacoSetup', () => ({ monacoTheme: () => 'light' }));

async function setup(readOnly = false) {
  const onEdit = vi.fn();
  render(
    <StepYamlSection
      model={modelHttpJq}
      step={httpStep}
      schema={schemaSample}
      readOnly={readOnly}
      onEdit={onEdit}
    />
  );
  const editor = await screen.findByTestId('monaco');
  const type = (text: string) =>
    fireEvent.change(editor, { target: { value: text } });
  return { onEdit, editor, type };
}

describe('StepYamlSection', () => {
  it('shows the YAML of the step at column 0', async () => {
    const { editor } = await setup();
    expect(editor).toHaveAttribute('data-language', 'yaml');
    expect(
      (editor as HTMLTextAreaElement).value.startsWith(
        'id: fetch\nname: fetch\n'
      )
    ).toBe(true);
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled();
  });

  it('shows the error of invalid YAML and emits nothing', async () => {
    const { onEdit, type } = await setup();
    type('id: fetch\nwith: [a\n');
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(screen.getByRole('alert')).toHaveTextContent(
      /^Line \d+, column \d+: /
    );
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('refuses a text that is not a mapping', async () => {
    const { onEdit, type } = await setup();
    type('- a\n- b\n');
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(screen.getByRole('alert')).toHaveTextContent(
      'The step must be a YAML mapping.'
    );
    expect(onEdit).not.toHaveBeenCalled();
  });

  it('applies a valid change with one replaceStepYaml', async () => {
    const { onEdit, type } = await setup();
    const yaml =
      'id: fetch\nname: fetch\naction: http.request\nwith:\n  method: POST\n  url: https://a';
    type(yaml);
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'replaceStepYaml',
      step: 'steps[0]',
      yaml,
    });
    expect(screen.queryByRole('alert')).toBeNull();
  });

  // The anchor of an alias can be elsewhere in the DAG.
  it('applies a change with an alias that the step does not define', async () => {
    const { onEdit, type } = await setup();
    const yaml = 'id: fetch\nname: fetch\nrun: echo hi\nretry_policy: *retry';
    type(yaml);
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onEdit).toHaveBeenCalledWith({
      kind: 'replaceStepYaml',
      step: 'steps[0]',
      yaml,
    });
  });

  it('applies a change with schema warnings and lists them', async () => {
    const { onEdit, type } = await setup();
    type('id: fetch\nname: fetch\nrun: echo hi\ncolour: red');
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));
    expect(onEdit).toHaveBeenCalledTimes(1);
    expect(
      screen.getByRole('list', { name: 'Schema warnings' })
    ).toHaveTextContent('colour');
  });

  it('resets the text', async () => {
    const { editor, type } = await setup();
    type('name: other');
    fireEvent.click(screen.getByRole('button', { name: 'Reset' }));
    expect((editor as HTMLTextAreaElement).value.startsWith('id: fetch')).toBe(
      true
    );
  });

  it('is read-only without buttons', async () => {
    const { editor } = await setup(true);
    expect(editor).toHaveAttribute('readonly');
    expect(screen.queryByRole('button', { name: 'Apply' })).toBeNull();
  });
});
