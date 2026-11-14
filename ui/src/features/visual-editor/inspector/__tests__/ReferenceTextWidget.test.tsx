// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { contextCandidates } from '../widgets/ReferenceTextWidget';
import { renderInspectorForm } from './formHarness';

const schema = {
  type: 'object',
  properties: {
    url: { type: 'string', title: 'url' },
    body: { type: 'string', title: 'body' },
    timeout: { type: 'integer', title: 'timeout' },
  },
};

const uiSchema = { body: { 'ui:widget': 'textarea' } };

describe('ReferenceTextWidget', () => {
  it('edits a string field through the reference input', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { url: 'https://a' },
      { uiSchema }
    );
    const input = screen.getByLabelText('url');
    expect(input).toHaveValue('https://a');
    fireEvent.change(input, { target: { value: '${params.URL}' } });
    expect(onChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ url: '${params.URL}' })
    );
  });

  it('gives undefined for an empty text', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { url: 'x' },
      { uiSchema }
    );
    onChange.mockClear();
    fireEvent.change(screen.getByLabelText('url'), { target: { value: '' } });
    expect(onChange).toHaveBeenCalled();
    expect(onChange.mock.lastCall?.[0]?.url).toBeUndefined();
  });

  it('renders long text as a text area', () => {
    renderInspectorForm(schema, {}, { uiSchema });
    expect(screen.getByLabelText('body').tagName).toBe('TEXTAREA');
    expect(screen.getByLabelText('url').tagName).toBe('INPUT');
  });

  it('keeps a number input for an integer field', () => {
    const { onChange } = renderInspectorForm(schema, {}, { uiSchema });
    const input = document.querySelector(
      'input[type="number"]'
    ) as HTMLInputElement;
    expect(input).not.toBeNull();
    fireEvent.change(input, { target: { value: '30' } });
    expect(onChange.mock.lastCall?.[0]?.timeout).toBe(30);
  });

  it('disables the inputs of a disabled form', () => {
    renderInspectorForm(schema, { url: 'x' }, { disabled: true, uiSchema });
    expect(screen.getByLabelText('url')).toBeDisabled();
  });

  it('reads the candidates of the form context', () => {
    expect(contextCandidates(undefined)).toEqual([]);
    expect(contextCandidates({ candidates: 'x' })).toEqual([]);
    const candidate = {
      token: '${params.A}',
      label: 'A',
      group: 'params' as const,
      known: 'static' as const,
    };
    expect(contextCandidates({ candidates: [candidate] })).toEqual([candidate]);
  });
});
