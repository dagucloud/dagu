// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ParamDefType } from '@/api/v1/schema';
import { validateParamValue } from '../paramValue';

describe('validateParamValue', () => {
  it.each([
    [{ type: ParamDefType.integer }, '3', undefined],
    [{ type: ParamDefType.integer }, '3.5', 'Must be an integer'],
    [{ type: ParamDefType.integer, minimum: 1 }, '0', 'Must be at least 1'],
    [{ type: ParamDefType.number, maximum: 1 }, '1.5', 'Must be at most 1'],
    [{ type: ParamDefType.number }, 'x', 'Must be a number'],
    [{ type: ParamDefType.number }, ' ', 'Must be a number'],
    [{ type: ParamDefType.boolean }, 'yes', 'Must be true or false'],
    [
      { type: ParamDefType.string, minLength: 2 },
      'a',
      'Must be at least 2 characters',
    ],
    [
      { type: ParamDefType.string, pattern: '^[a-z]+$' },
      'A',
      'Does not match the required pattern',
    ],
    [
      { type: ParamDefType.string, enum: ['dev', 'prod'] },
      'prd',
      'Must be one of the allowed values',
    ],
    [{ type: ParamDefType.integer, enum: [1, 2] }, '2', undefined],
  ])('checks %j against %j', (param, value, want) => {
    expect(validateParamValue(param, value)).toBe(want);
  });
});
