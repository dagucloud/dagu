// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { KeyValueEditor, type KeyValueEditorProps } from '../widgets';
import { renderInspectorForm } from './formHarness';
import {
  candidatesSample,
  referenceList,
  typeOpenReference,
} from './referenceHarness';

// The tests of the rows use no references.
function Editor(props: Omit<KeyValueEditorProps, 'candidates'>) {
  return <KeyValueEditor candidates={[]} {...props} />;
}

describe('KeyValueEditor', () => {
  it('offers references in a value, and sends the chosen one', () => {
    const onChange = vi.fn();
    render(
      <KeyValueEditor
        value={{ X: 'a' }}
        readOnly={false}
        onChange={onChange}
        candidates={candidatesSample}
      />
    );
    const value = screen.getByLabelText('Value');
    typeOpenReference(value);
    expect(referenceList()).toBeInTheDocument();
    fireEvent.keyDown(value, { key: 'Enter' });
    fireEvent.blur(value);
    expect(onChange).toHaveBeenCalledWith({ X: '${params.API_URL}' });
  });

  it('offers no references in a key', () => {
    render(
      <KeyValueEditor
        value={{ X: 'a' }}
        readOnly={false}
        onChange={vi.fn()}
        candidates={candidatesSample}
      />
    );
    typeOpenReference(screen.getByLabelText('Key'));
    expect(referenceList()).not.toBeInTheDocument();
  });

  it('sends the new object when an input loses the focus', () => {
    const onChange = vi.fn();
    render(
      <Editor value={{ X: 'a', Y: 'b' }} readOnly={false} onChange={onChange} />
    );
    const values = screen.getAllByLabelText('Value');
    fireEvent.change(values[0]!, { target: { value: 'c' } });
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.blur(values[0]!);
    expect(onChange).toHaveBeenCalledWith({ X: 'c', Y: 'b' });
  });

  it('sends nothing when nothing changed', () => {
    const onChange = vi.fn();
    render(<Editor value={{ X: 'a' }} readOnly={false} onChange={onChange} />);
    fireEvent.blur(screen.getByLabelText('Value'));
    expect(onChange).not.toHaveBeenCalled();
  });

  it('removes a row at once', () => {
    const onChange = vi.fn();
    render(
      <Editor value={{ X: 'a', Y: 'b' }} readOnly={false} onChange={onChange} />
    );
    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]!);
    expect(onChange).toHaveBeenCalledWith({ Y: 'b' });
  });

  it('adds a row and sends it once it has a key', () => {
    const onChange = vi.fn();
    render(<Editor value={{}} readOnly={false} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'Add entry' }));
    fireEvent.blur(screen.getByLabelText('Key'));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('Key'), {
      target: { value: 'TOKEN' },
    });
    fireEvent.change(screen.getByLabelText('Value'), {
      target: { value: '1' },
    });
    fireEvent.blur(screen.getByLabelText('Value'));
    expect(onChange).toHaveBeenCalledWith({ TOKEN: '1' });
  });

  // Names of Object.prototype members are ordinary keys.
  it('sends a key that matches an inherited property', () => {
    const onChange = vi.fn();
    render(<Editor value={{}} readOnly={false} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button', { name: 'Add entry' }));
    fireEvent.change(screen.getByLabelText('Key'), {
      target: { value: 'constructor' },
    });
    fireEvent.change(screen.getByLabelText('Value'), {
      target: { value: '1' },
    });
    fireEvent.blur(screen.getByLabelText('Value'));
    expect(onChange).toHaveBeenCalledWith({ constructor: '1' });
  });

  it('keeps the key order on a rename', () => {
    const onChange = vi.fn();
    render(
      <Editor value={{ A: '1', B: '2' }} readOnly={false} onChange={onChange} />
    );
    const keys = screen.getAllByLabelText('Key');
    fireEvent.change(keys[0]!, { target: { value: 'Z' } });
    fireEvent.blur(keys[0]!);
    expect(Object.keys(onChange.mock.lastCall?.[0] ?? {})).toEqual(['Z', 'B']);
  });

  // A map holds one value for each key: sending rows that share a key would
  // drop one of the values while both rows stay on screen.
  it('sends nothing while two rows have the same key', () => {
    const onChange = vi.fn();
    render(
      <Editor
        value={{ A: 'one', B: 'two' }}
        readOnly={false}
        onChange={onChange}
      />
    );
    const keys = screen.getAllByLabelText('Key');
    fireEvent.change(keys[0]!, { target: { value: 'B' } });
    fireEvent.blur(keys[0]!);
    expect(onChange).not.toHaveBeenCalled();
    expect(keys[0]).toHaveAttribute('aria-invalid', 'true');
    expect(keys[1]).toHaveAttribute('aria-invalid', 'true');

    fireEvent.change(keys[0]!, { target: { value: 'C' } });
    fireEvent.blur(keys[0]!);
    expect(onChange).toHaveBeenCalledWith({ C: 'one', B: 'two' });
    expect(keys[0]).not.toHaveAttribute('aria-invalid');
  });

  it('disables every input when read-only', () => {
    render(<Editor value={{ X: 'a' }} readOnly onChange={vi.fn()} />);
    expect(screen.getByLabelText('Key')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove' })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Add entry' })).toBeNull();
  });

  it('shows new rows when the value changes from outside', () => {
    const { rerender } = render(
      <Editor value={{ X: 'a' }} readOnly={false} onChange={vi.fn()} />
    );
    rerender(
      <Editor value={{ X: 'a', Y: 'b' }} readOnly={false} onChange={vi.fn()} />
    );
    expect(screen.getAllByLabelText('Key')).toHaveLength(2);
  });
});

describe('KeyValueField', () => {
  const schema = {
    type: 'object',
    properties: {
      headers: {
        type: 'object',
        title: 'headers',
        additionalProperties: { type: 'string' },
      },
    },
  };

  const uiSchema = { headers: { 'ui:field': 'KeyValue' } };

  it('offers the references of the form in a value', () => {
    renderInspectorForm(
      schema,
      { headers: { X: 'a' } },
      { uiSchema, candidates: candidatesSample }
    );
    typeOpenReference(screen.getByLabelText('Value'));
    expect(referenceList()).toHaveTextContent('API_URL');
  });

  it('removes the key of a removed row from the form data', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { headers: { X: 'a', Y: 'b' } },
      { uiSchema }
    );
    expect(screen.getByRole('group', { name: 'headers' })).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Remove' })[0]!);
    expect(onChange).toHaveBeenLastCalledWith({ headers: { Y: 'b' } });
  });

  it('removes the map when its last row goes', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { headers: { X: 'a' } },
      { uiSchema }
    );
    onChange.mockClear();
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    expect(onChange).toHaveBeenCalled();
    expect(onChange.mock.lastCall?.[0]?.headers).toBeUndefined();
  });
});
