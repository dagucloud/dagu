// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { insertIntoField, useLastFocusedField } from '../insertIntoField';

function Harness({
  onReady,
  onReset,
}: {
  onReady(get: () => unknown): void;
  onReset?(reset: () => void): void;
}) {
  const [url, setUrl] = useState('https://example.com/');
  const lastField = useLastFocusedField();
  onReady(lastField.get);
  onReset?.(lastField.reset);
  return (
    <>
      <div onFocus={lastField.onFocus}>
        <input
          aria-label="URL"
          value={url}
          onChange={(event) => setUrl(event.target.value)}
        />
        <input aria-label="Count" type="number" defaultValue={1} />
      </div>
      <input aria-label="Outside" defaultValue="" />
      <output aria-label="State">{url}</output>
    </>
  );
}

describe('insertIntoField', () => {
  it('inserts the token at the caret of the last focused field', () => {
    let getField: () => unknown = () => null;
    render(<Harness onReady={(get) => (getField = get)} />);
    const field = screen.getByLabelText('URL') as HTMLInputElement;

    fireEvent.focusIn(field);
    field.setSelectionRange(8, 8);
    insertIntoField(field, '${params.host}');

    expect(screen.getByLabelText('State')).toHaveTextContent(
      'https://${params.host}example.com/'
    );
    expect(field.selectionStart).toBe(8 + '${params.host}'.length);
    expect(getField()).toBe(field);
  });

  it('ignores fields outside the panel and non-text fields', () => {
    let getField: () => unknown = () => null;
    render(<Harness onReady={(get) => (getField = get)} />);

    fireEvent.focusIn(screen.getByLabelText('Count'));
    fireEvent.focusIn(screen.getByLabelText('Outside'));

    expect(getField()).toBeNull();
  });

  // The form can turn read-only after a field had the focus.
  it('gives no field once it cannot be edited', () => {
    let getField: () => unknown = () => null;
    render(<Harness onReady={(get) => (getField = get)} />);
    const field = screen.getByLabelText('URL') as HTMLInputElement;

    fireEvent.focusIn(field);
    field.readOnly = true;

    expect(getField()).toBeNull();
  });

  it('forgets the field on reset', () => {
    let getField: () => unknown = () => null;
    let reset = () => {};
    render(
      <Harness
        onReady={(get) => (getField = get)}
        onReset={(fn) => (reset = fn)}
      />
    );
    fireEvent.focusIn(screen.getByLabelText('URL'));
    expect(getField()).not.toBeNull();
    reset();
    expect(getField()).toBeNull();
  });
});
