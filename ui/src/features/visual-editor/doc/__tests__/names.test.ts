// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { uniqueStepName, validateStepId, validateStepName } from '../names';

describe('validateStepName', () => {
  const valid = [
    'build',
    'Build image',
    'fetch-data.v2',
    'étape 1',
    '日本語',
    'a'.repeat(255),
    '日'.repeat(85),
    'env',
  ];
  it.each(valid)('accepts %j', (name) => {
    expect(validateStepName(name)).toBeNull();
  });

  const invalid: Array<[string, string]> = [
    ['', 'Step name must not be empty.'],
    ['   ', 'Step name must not be empty.'],
    [' build', 'Step name must not start or end with whitespace.'],
    ['build\n', 'Step name must not start or end with whitespace.'],
    ['a'.repeat(256), 'Step name must be at most 255 bytes.'],
    ['日'.repeat(86), 'Step name must be at most 255 bytes.'],
    ['__agent__', '"__agent__" is a reserved step name.'],
    ['ask_user', '"ask_user" is a reserved step name.'],
  ];
  it.each(invalid)('rejects %j', (name, message) => {
    expect(validateStepName(name)).toBe(message);
  });
});

describe('validateStepId', () => {
  const valid = ['build', 'Build_2', 'a', 'x_', 'a'.repeat(40), 'steps'];
  it.each(valid)('accepts %j', (id) => {
    expect(validateStepId(id)).toBeNull();
  });

  const format =
    "Step ID must start with a letter and use only letters, digits and '_'.";
  const invalid: Array<[string, string]> = [
    ['', format],
    ['1build', format],
    ['_build', format],
    ['build-step', format],
    ['build step', format],
    ['étape', format],
    [' build', format],
    ['a'.repeat(41), 'Step ID must be at most 40 bytes.'],
    ['env', '"env" is a reserved word and cannot be a step ID.'],
    ['Params', '"Params" is a reserved word and cannot be a step ID.'],
    ['STDOUT', '"STDOUT" is a reserved word and cannot be a step ID.'],
    ['outputs', '"outputs" is a reserved word and cannot be a step ID.'],
  ];
  it.each(invalid)('rejects %j', (id, message) => {
    expect(validateStepId(id)).toBe(message);
  });
});

describe('uniqueStepName', () => {
  const cases: Array<[string, string[], string]> = [
    ['build', [], 'build'],
    ['build', ['build'], 'build_2'],
    ['build', ['build', 'build_2'], 'build_3'],
    ['build', ['build_2'], 'build'],
    ['http.request', [], 'http_request'],
    ['http.request', ['http_request'], 'http_request_2'],
    ['Build image', [], 'Build_image'],
    ['  --x--  ', [], 'x'],
    ['', [], 'step'],
    ['...', ['step'], 'step_2'],
    ['étape.1', [], 'étape_1'],
    // A combining mark stays with its letter.
    ['\u0915\u093f.x', [], '\u0915\u093f_x'],
    ['e\u0301tape', [], 'e\u0301tape'],
    ['ask_user', [], 'ask_user_2'],
    ['__agent__', [], 'agent'],
  ];
  it.each(cases)('%j with %j gives %j', (base, taken, expected) => {
    expect(uniqueStepName(base, taken)).toBe(expected);
  });

  it('accepts any iterable', () => {
    expect(uniqueStepName('a', new Set(['a']))).toBe('a_2');
    expect(uniqueStepName('a', new Map([['a', 1]]).keys())).toBe('a_2');
  });

  it('cuts a long base to a valid name', () => {
    const name = uniqueStepName('x'.repeat(300), []);
    expect(name).toBe('x'.repeat(200));
    const next = uniqueStepName('x'.repeat(300), [name]);
    expect(next).toBe(`${'x'.repeat(200)}_2`);
    expect(validateStepName(next)).toBeNull();
    expect(uniqueStepName('日'.repeat(100), [])).toBe('日'.repeat(66));
  });
});
