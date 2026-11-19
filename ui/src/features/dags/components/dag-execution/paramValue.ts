// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components, ParamDefType } from '../../../../api/v1/schema';

type ParamDef = components['schemas']['ParamDef'];

/** The constraints of a typed param that one value is checked against. */
type ParamConstraints = Pick<
  ParamDef,
  | 'type'
  | 'enum'
  | 'minimum'
  | 'maximum'
  | 'minLength'
  | 'maxLength'
  | 'pattern'
>;

/**
 * Returns why a value given as text breaks the constraints of its param, or
 * undefined when it meets them. Whether a value is required is the caller's
 * concern.
 */
export function validateParamValue(
  param: ParamConstraints,
  value: string
): string | undefined {
  switch (param.type) {
    case ParamDefType.string:
      if (param.minLength !== undefined && value.length < param.minLength) {
        return `Must be at least ${param.minLength} characters`;
      }
      if (param.maxLength !== undefined && value.length > param.maxLength) {
        return `Must be at most ${param.maxLength} characters`;
      }
      if (param.pattern) {
        try {
          if (!new RegExp(param.pattern).test(value)) {
            return 'Does not match the required pattern';
          }
        } catch {
          return 'Invalid validation pattern';
        }
      }
      if (
        param.enum &&
        !param.enum.some((item) => typeof item === 'string' && item === value)
      ) {
        return 'Must be one of the allowed values';
      }
      return undefined;

    case ParamDefType.integer:
      if (!/^-?\d+$/.test(value.trim())) {
        return 'Must be an integer';
      }
      return validateNumber(param, Number(value));

    case ParamDefType.number: {
      const trimmed = value.trim();
      const number = Number(trimmed);
      if (trimmed === '' || Number.isNaN(number)) {
        return 'Must be a number';
      }
      return validateNumber(param, number);
    }

    case ParamDefType.boolean:
      if (value !== 'true' && value !== 'false') {
        return 'Must be true or false';
      }
      return undefined;
  }
  return undefined;
}

function validateNumber(
  param: ParamConstraints,
  number: number
): string | undefined {
  if (param.minimum !== undefined && number < param.minimum) {
    return `Must be at least ${param.minimum}`;
  }
  if (param.maximum !== undefined && number > param.maximum) {
    return `Must be at most ${param.maximum}`;
  }
  if (
    param.enum &&
    !param.enum.some(
      (item) => typeof item === 'number' && Number(item) === number
    )
  ) {
    return 'Must be one of the allowed values';
  }
  return undefined;
}
