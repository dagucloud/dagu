// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { JSONSchema } from '../../contracts';
import { isStringOrMap } from '../widgets/StringOrMap';
import { buildUiSchema } from '../widgets';
import { renderInspectorForm } from './formHarness';

// The `params` of `dag.run`: a string, a list or a map.
const schema: JSONSchema = {
  type: 'object',
  properties: {
    dag: { type: 'string' },
    params: {
      oneOf: [
        { type: 'string' },
        {
          type: 'array',
          items: {
            oneOf: [
              { type: 'string' },
              { type: 'object', additionalProperties: true },
            ],
          },
        },
        { type: 'object', additionalProperties: true },
      ],
      description: 'Parameters passed to the child DAG.',
    },
  },
};

describe('StringOrMap', () => {
  it('knows a union of a string and a free object', () => {
    expect(isStringOrMap(schema.properties!.params as never)).toBe(true);
    expect(isStringOrMap({ type: 'string' })).toBe(false);
    expect(
      isStringOrMap({ oneOf: [{ type: 'integer' }, { type: 'string' }] })
    ).toBe(false);
    expect(buildUiSchema(schema).params).toMatchObject({
      'ui:field': 'StringOrMap',
    });
  });

  // A map used to show as "[object Object]" in a text input.
  it('shows a map as key and value rows', () => {
    renderInspectorForm(schema, { dag: 'greeter', params: { NAME: 'world' } });

    expect(screen.queryByDisplayValue('[object Object]')).toBeNull();
    expect(screen.getByDisplayValue('NAME')).toBeInTheDocument();
    expect(screen.getByDisplayValue('world')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Key/value' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  it('sends the whole map when a value changes', () => {
    const { onChange } = renderInspectorForm(schema, {
      dag: 'greeter',
      params: { NAME: 'world', COUNT: '2' },
    });

    const input = screen.getByDisplayValue('world');
    fireEvent.change(input, { target: { value: 'there' } });
    fireEvent.blur(input);

    expect(onChange).toHaveBeenLastCalledWith({
      dag: 'greeter',
      params: { NAME: 'there', COUNT: '2' },
    });
  });

  it('shows a string as text', () => {
    renderInspectorForm(schema, { dag: 'greeter', params: 'NAME=world' });

    expect(screen.getByDisplayValue('NAME=world')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Text' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
  });

  it('sends nothing when the mode changes, and a value when one is entered', () => {
    const { onChange } = renderInspectorForm(schema, { dag: 'greeter' });

    fireEvent.click(screen.getByRole('button', { name: 'Text' }));
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText('params'), {
      target: { value: 'NAME=x' },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      dag: 'greeter',
      params: 'NAME=x',
    });
  });

  it('leaves a list or a map with a number to the YAML section', () => {
    const { unmount } = renderInspectorForm(schema, {
      dag: 'greeter',
      params: ['NAME=world'],
    });
    expect(screen.getByText('Edit this field in YAML')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Text' })).toBeNull();
    unmount();

    renderInspectorForm(schema, { dag: 'greeter', params: { COUNT: 3 } });
    expect(screen.getByText('Edit this field in YAML')).toBeInTheDocument();
  });
});
