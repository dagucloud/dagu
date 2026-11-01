// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { RJSFSchema } from '@rjsf/utils';
import { fireEvent, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { getActionWithSchema } from '../withSchema';
import { scalarUnionKind, suggestedValues } from '../widgets';
import { renderInspectorForm } from './formHarness';
import { realSchema } from './realSchema';

describe('scalarUnionKind', () => {
  it.each<[RJSFSchema, string | null]>([
    [{ oneOf: [{ type: 'integer' }, { type: 'string' }] }, 'integer'],
    [{ anyOf: [{ type: 'number' }, { type: 'string' }] }, 'number'],
    [{ oneOf: [{ type: 'boolean' }, { type: 'string' }] }, 'boolean'],
    [{ oneOf: [{ type: ['integer', 'string'] }] }, 'integer'],
    [{ oneOf: [{ type: 'string' }, { type: 'array' }] }, null],
    [{ oneOf: [{ type: 'integer' }, { type: 'boolean' }] }, null],
    [{ type: 'string' }, null],
  ])('%j gives %s', (schema, kind) => {
    expect(scalarUnionKind(schema)).toBe(kind);
  });
});

const schema = {
  type: 'object',
  properties: {
    limit: { title: 'limit', oneOf: [{ type: 'integer' }, { type: 'string' }] },
    raw: { title: 'raw', oneOf: [{ type: 'boolean' }, { type: 'string' }] },
    data: { title: 'data', description: 'Inline value.' },
  },
};

const replace = {
  'ui:field': 'ValueOrReference',
  'ui:fieldReplacesAnyOrOneOf': true,
};
const uiSchema = {
  limit: replace,
  raw: replace,
  data: { 'ui:field': 'FreeValue' },
};

describe('ValueOrReferenceField', () => {
  it('edits a number in value mode', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { limit: 2 },
      { uiSchema }
    );
    const input = screen.getByLabelText('limit');
    expect(input).toHaveAttribute('type', 'number');
    fireEvent.change(input, { target: { value: '5' } });
    expect(onChange.mock.lastCall?.[0]?.limit).toBe(5);
  });

  // A number input takes the exponent form: 1e3 is a thousand, not 1.
  it('reads the exponent form of a number', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { limit: 2 },
      { uiSchema }
    );
    fireEvent.change(screen.getByLabelText('limit'), {
      target: { value: '1e3' },
    });
    expect(onChange.mock.lastCall?.[0]?.limit).toBe(1000);
  });

  it('starts in reference mode for a string and keeps it a string', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { limit: '${RETRIES}' },
      { uiSchema }
    );
    expect(
      screen.getAllByRole('button', { name: 'Reference' })[0]
    ).toHaveAttribute('aria-pressed', 'true');
    const input = screen.getByLabelText('limit');
    expect(input).toHaveValue('${RETRIES}');
    fireEvent.change(input, { target: { value: '${MAX}' } });
    expect(onChange.mock.lastCall?.[0]?.limit).toBe('${MAX}');
  });

  it('switches mode without a change', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { limit: 2 },
      { uiSchema }
    );
    const calls = onChange.mock.calls.length;
    fireEvent.click(screen.getAllByRole('button', { name: 'Reference' })[0]!);
    expect(onChange.mock.calls.length).toBe(calls);
    expect(screen.getByLabelText('limit')).toHaveAttribute('type', 'text');
  });

  it('edits a boolean with a switch', () => {
    const { onChange } = renderInspectorForm(
      schema,
      { raw: false },
      { uiSchema }
    );
    fireEvent.click(screen.getByRole('switch', { name: 'raw' }));
    expect(onChange.mock.lastCall?.[0]?.raw).toBe(true);
  });

  it('disables the toggle and the input of a disabled form', () => {
    renderInspectorForm(schema, { limit: 2 }, { disabled: true, uiSchema });
    expect(screen.getByLabelText('limit')).toBeDisabled();
    expect(
      screen.getAllByRole('button', { name: 'Reference' })[0]
    ).toBeDisabled();
  });
});

describe('FreeValueField', () => {
  it('edits a string value', () => {
    const { onChange } = renderInspectorForm(
      schema,
      {
        data: '${steps.a.outputs.body}',
      },
      { uiSchema }
    );
    fireEvent.change(screen.getByLabelText('data'), {
      target: { value: '[1]' },
    });
    expect(onChange.mock.lastCall?.[0]?.data).toBe('[1]');
  });

  it('shows another value as JSON', () => {
    renderInspectorForm(schema, { data: { a: 1 } }, { uiSchema });
    expect(screen.queryByLabelText('data')).toBeNull();
    expect(screen.getByText('Edit this field in YAML')).toBeInTheDocument();
  });
});

describe('suggestedValues', () => {
  it.each<[RJSFSchema, string[] | null]>([
    [
      { anyOf: [{ type: 'string', enum: ['a', 'b'] }, { type: 'string' }] },
      ['a', 'b'],
    ],
    [
      {
        anyOf: [
          { type: 'string', enum: ['a'] },
          { type: 'string', enum: ['a', 'c'] },
          { type: 'string' },
        ],
      },
      ['a', 'c'],
    ],
    [{ oneOf: [{ type: 'string', enum: ['a'] }, { type: 'string' }] }, null],
    [{ anyOf: [{ type: 'string', enum: ['a'] }] }, null],
    [{ anyOf: [{ type: 'string' }, { type: 'string' }] }, null],
    [{ anyOf: [{ type: 'string', enum: ['a'] }, { type: 'integer' }] }, null],
    [{ type: 'string', enum: ['a'] }, null],
  ])('%j gives %j', (schema, values) => {
    expect(suggestedValues(schema)).toEqual(values);
  });
});

describe('SuggestedTextField', () => {
  const providerSchema = {
    type: 'object',
    properties: {
      provider: {
        title: 'provider',
        anyOf: [
          { type: 'string', enum: ['claude', 'codex'] },
          { type: 'string' },
        ],
      },
    },
  };

  it('writes a suggested value', () => {
    const { onChange } = renderInspectorForm(providerSchema, {});
    fireEvent.focus(screen.getByLabelText('provider'));
    fireEvent.click(screen.getByRole('option', { name: 'codex' }));
    expect(onChange.mock.lastCall?.[0]?.provider).toBe('codex');
  });

  it('keeps a value that is not suggested', () => {
    const { onChange } = renderInspectorForm(providerSchema, {});
    fireEvent.change(screen.getByLabelText('provider'), {
      target: { value: '${HARNESS_PROVIDER}' },
    });
    expect(onChange.mock.lastCall?.[0]?.provider).toBe('${HARNESS_PROVIDER}');
  });
});

describe.skipIf(realSchema === null)('the providers of harness.run', () => {
  it('suggests the builtin providers, also for a fallback', () => {
    const withSchema = getActionWithSchema(realSchema!, 'harness.run', [])!;
    renderInspectorForm(withSchema, {
      prompt: 'Write tests',
      fallback: [{ provider: 'codex' }],
    });
    const [primary, fallback] = screen.getAllByRole('combobox', {
      name: 'provider',
    });
    fireEvent.focus(primary!);
    expect(screen.getByRole('option', { name: 'claude' })).toBeInTheDocument();
    fireEvent.focus(fallback!);
    expect(fallback).toHaveAttribute('aria-expanded', 'true');
    const listbox = document.getElementById(
      fallback!.getAttribute('aria-controls')!
    )!;
    expect(
      within(listbox).getByRole('option', { name: 'codex' })
    ).toBeInTheDocument();
  });
});
