// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { checkStepSchema, checkStepSchemaErrors } from '../stepSchemaCheck';
import { schemaSample } from './fixtures';
import { realSchema } from './realSchema';

describe('checkStepSchema', () => {
  it('gives no message for a valid step', () => {
    expect(
      checkStepSchema(schemaSample, {
        name: 'fetch',
        action: 'http.request',
        with: { method: 'GET', url: 'https://a' },
      })
    ).toEqual([]);
  });

  it('names an unknown key', () => {
    const messages = checkStepSchema(schemaSample, {
      name: 'a',
      run: 'x',
      colour: 'red',
    });
    expect(messages.some((m) => m.includes('colour'))).toBe(true);
  });

  // `constructor` is a member of Object.prototype, not a step property.
  it('names an unknown key that every object has', () => {
    const messages = checkStepSchema(schemaSample, {
      name: 'a',
      run: 'x',
      constructor: 'x',
    });
    expect(messages.some((m) => m.includes('constructor'))).toBe(true);
  });

  it('reports a wrong type with its path', () => {
    const messages = checkStepSchema(schemaSample, {
      name: 'a',
      timeout_sec: 'soon',
    });
    expect(messages.some((m) => m.startsWith('/timeout_sec'))).toBe(true);
  });

  it('gives no message for a schema without a step definition', () => {
    expect(checkStepSchema({ type: 'object' }, { anything: true })).toEqual([]);
  });

  it('checks the with block of the step action', () => {
    const messages = checkStepSchema(schemaSample, {
      name: 'a',
      action: 'http.request',
      with: { method: 'GET' },
    });
    expect(messages.some((m) => m.includes('url'))).toBe(true);
  });

  // The first compile of a DAG schema part is slow in jsdom on a busy machine.
  it.skipIf(!realSchema)(
    'checks a step against the DAG schema',
    { timeout: 30000 },
    () => {
      expect(
        checkStepSchema(realSchema!, { name: 'a', run: 'echo hi' })
      ).toEqual([]);
      expect(
        checkStepSchema(realSchema!, { name: 'a', run: 'x', colour: 1 }).length
      ).toBeGreaterThan(0);
      expect(
        checkStepSchema(realSchema!, {
          name: 'a',
          action: 'http.request',
          with: { method: 'GET', url: 3 },
        }).some((m) => m.startsWith('/with/url'))
      ).toBe(true);
    }
  );

  // A run step takes the shell settings in `with`, as the DAG schema says.
  it.skipIf(!realSchema)(
    'checks the with block of a run step',
    { timeout: 30000 },
    () => {
      expect(
        checkStepSchema(realSchema!, {
          name: 'a',
          run: 'echo hi',
          with: { shell: 'bash' },
        })
      ).toEqual([]);
      const messages = checkStepSchema(realSchema!, {
        name: 'a',
        run: 'echo hi',
        with: { shell_args: 42, colour: 'red' },
      });
      expect(messages.some((m) => m.startsWith('/with/shell_args'))).toBe(true);
      expect(messages.some((m) => m.includes('colour'))).toBe(true);
    }
  );
});

describe('checkStepSchemaErrors', () => {
  it('gives every failed rule, with its path and the missing key', () => {
    const errors = checkStepSchemaErrors(schemaSample, {
      name: 'a',
      action: 'http.request',
      with: { method: 'GET' },
      timeout_sec: 'soon',
      colour: 'red',
    });
    expect(errors).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          instancePath: '/with',
          keyword: 'required',
          property: 'url',
        }),
        expect.objectContaining({
          instancePath: '/timeout_sec',
          keyword: 'type',
        }),
        expect.objectContaining({
          keyword: 'additionalProperties',
          property: 'colour',
        }),
      ])
    );
  });

  it('gives none for a valid step and for a value that is no step', () => {
    expect(
      checkStepSchemaErrors(schemaSample, { name: 'a', run: 'x' })
    ).toEqual([]);
    expect(checkStepSchemaErrors(schemaSample, 'x')).toEqual([]);
  });
});
