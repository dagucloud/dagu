// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { parseDagDoc } from '../../doc/parse';
import { scopeHttpJq } from '../../testing/fixtures';
import type { CaretEditor } from '../editorCaret';
import { YamlReferenceButton } from '../YamlReferenceButton';

function fakeEditor(text: string, offset: number) {
  const position = { lineNumber: 1, column: 1 };
  const lines = text.slice(0, offset).split('\n');
  position.lineNumber = lines.length;
  position.column = lines[lines.length - 1]!.length + 1;
  const editor = {
    getModel: () => ({
      getValue: () => text,
      getOffsetAt: () => offset,
      getPositionAt: () => position,
    }),
    getPosition: () => position,
    setPosition: vi.fn(),
    getSelection: () => ({
      startLineNumber: position.lineNumber,
      startColumn: position.column,
      endLineNumber: position.lineNumber,
      endColumn: position.column,
    }),
    executeEdits: vi.fn(() => true),
    pushUndoStop: vi.fn(() => true),
    trigger: vi.fn(),
    focus: vi.fn(),
  };
  return { editor: editor as unknown as CaretEditor, raw: editor };
}

const text = 'steps:\n  - name: a\n    run: echo hi\n';
const inValue = text.indexOf('echo hi');

function setup(offset: number) {
  const { editor, raw } = fakeEditor(text, offset);
  render(
    <YamlReferenceButton
      editorRef={{ current: editor }}
      getState={() => ({ scope: scopeHttpJq })}
    />
  );
  const open = () =>
    fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
  return { raw, open };
}

describe('YamlReferenceButton', () => {
  it('lists what the field at the caret can read, without typing', () => {
    const { open } = setup(inValue);
    expect(screen.queryByRole('listbox')).toBeNull();
    open();
    expect(screen.getByRole('listbox')).toHaveTextContent('${params.API_URL}');
    expect(screen.getByLabelText('Search references')).toHaveFocus();
  });

  it('inserts the picked token at the caret and closes', () => {
    const { raw, open } = setup(inValue);
    open();
    fireEvent.click(screen.getByRole('option', { name: /API_URL/ }));
    expect(raw.executeEdits).toHaveBeenCalledWith(
      'reference',
      expect.arrayContaining([
        expect.objectContaining({ text: '${params.API_URL}' }),
      ])
    );
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  // One edit call, so that one undo takes back both the token and the output.
  it('publishes the output of an upstream step with the same pick', () => {
    const doc = [
      'steps:',
      '  - id: fetch',
      '    run: echo hi',
      '  - id: use',
      '    run: echo x',
      '',
    ].join('\n');
    const offset = doc.indexOf('echo x');
    const { editor, raw } = fakeEditor(doc, offset);
    const base = scopeHttpJq.steps[0]!;
    render(
      <YamlReferenceButton
        editorRef={{ current: editor }}
        getState={() => ({
          model: parseDagDoc(doc),
          scope: {
            ...scopeHttpJq,
            steps: [
              {
                ...base,
                name: 'fetch',
                id: 'fetch',
                stepPath: 'steps[0]',
                upstream: [],
                outputs: [],
              },
              {
                ...base,
                name: 'use',
                id: 'use',
                stepPath: 'steps[1]',
                upstream: ['fetch'],
                outputs: [],
              },
            ],
          },
        })}
      />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Insert reference' }));
    fireEvent.click(screen.getByRole('option', { name: /fetch\.stdout/ }));
    expect(raw.executeEdits).toHaveBeenCalledTimes(1);
    const [, edits] = raw.executeEdits.mock.calls[0] as unknown as [
      string,
      { text: string }[],
    ];
    expect(edits.map((edit) => edit.text).join('\n')).toContain(
      '${steps.fetch.outputs.stdout}'
    );
    expect(edits.map((edit) => edit.text).join('\n')).toContain('from: stdout');
  });

  it('gives the focus back to the editor on Escape', () => {
    const { raw, open } = setup(inValue);
    open();
    fireEvent.keyDown(screen.getByLabelText('Search references'), {
      key: 'Escape',
    });
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(raw.focus).toHaveBeenCalledTimes(1);
  });

  it('says what to do when the caret is not in a value', () => {
    const { open } = setup(text.indexOf('run:') + 1);
    open();
    expect(
      screen.getByText('Put the cursor in a field value')
    ).toBeInTheDocument();
    expect(screen.queryAllByRole('option')).toHaveLength(0);
  });

  it('closes when the focus leaves', () => {
    const { open } = setup(inValue);
    open();
    fireEvent.blur(screen.getByLabelText('Search references'), {
      relatedTarget: document.body,
    });
    expect(screen.queryByRole('listbox')).toBeNull();
  });
});
