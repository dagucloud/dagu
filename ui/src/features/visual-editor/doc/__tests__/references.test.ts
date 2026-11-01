// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  mentionsStep,
  outputReferenceIds,
  rewriteStepReferences,
} from '../ops/references';

// [input, expected output of renaming `fetch` to `load`]
const rewritten: Array<[string, string]> = [
  // canonical form
  ['${steps.fetch.outputs.body}', '${steps.load.outputs.body}'],
  ['${ steps.fetch.outputs.body }', '${ steps.load.outputs.body }'],
  ['url=${steps.fetch.outputs.url}&x=1', 'url=${steps.load.outputs.url}&x=1'],
  // legacy braced form
  ['${fetch.stdout}', '${load.stdout}'],
  ['${fetch.stderr}', '${load.stderr}'],
  ['${fetch.exit_code}', '${load.exit_code}'],
  ['${fetch.exitCode}', '${load.exitCode}'],
  ['${fetch.output}', '${load.output}'],
  ['${fetch.outputs}', '${load.outputs}'],
  ['${fetch.stdout:0:5}', '${load.stdout:0:5}'],
  ['${fetch.stdout:3}', '${load.stdout:3}'],
  ['${fetch.stdout:0:}', '${load.stdout:0:}'],
  ['${fetch.output.items[0]}', '${load.output.items[0]}'],
  ['${fetch.outputs.body.items[0].id}', '${load.outputs.body.items[0].id}'],
  ['${fetch.outputs[0]}', '${load.outputs[0]}'],
  // legacy unbraced form
  ['echo $fetch.stdout', 'echo $load.stdout'],
  ['$fetch.exit_code', '$load.exit_code'],
  ['$fetch.output.items[0]', '$load.output.items[0]'],
  // several tokens in one string
  [
    '${steps.fetch.outputs.a} ${fetch.stdout} $fetch.stderr ${steps.fetch.outputs.b}',
    '${steps.load.outputs.a} ${load.stdout} $load.stderr ${steps.load.outputs.b}',
  ],
  [
    '${steps.fetch.outputs.a}${steps.other.outputs.a}${fetch.stdout}',
    '${steps.load.outputs.a}${steps.other.outputs.a}${load.stdout}',
  ],
  // multi-line text
  [
    'set -e\ncurl ${steps.fetch.outputs.url}\necho "${fetch.stdout}" # fetch\nprintf $fetch.exit_code\n',
    'set -e\ncurl ${steps.load.outputs.url}\necho "${load.stdout}" # fetch\nprintf $load.exit_code\n',
  ],
  // unicode around tokens
  [
    'été ${steps.fetch.outputs.x} 日本語 ${fetch.stdout} ✓',
    'été ${steps.load.outputs.x} 日本語 ${load.stdout} ✓',
  ],
  // even backslash run does not escape
  ['\\\\${steps.fetch.outputs.x}', '\\\\${steps.load.outputs.x}'],
  ['\\\\${fetch.stdout}', '\\\\${load.stdout}'],
];

const unchanged: string[] = [
  // plain text and other namespaces
  'echo fetch',
  'fetch',
  'fetch.stdout',
  '${params.fetch}',
  '${env.fetch}',
  '${consts.fetch}',
  '${steps.fetch}',
  '${stepsx}',
  '${stepsx.fetch.outputs.body}',
  // bare names are env or variable references, not step references
  '${fetch}',
  '$fetch',
  '${FETCH.stdout}',
  // other steps and prefix names
  '${steps.other.outputs.body}',
  '${steps.fetch2.outputs.body}',
  '${steps.xfetch.outputs.body}',
  '${fetch2.stdout}',
  '${xfetch.stdout}',
  '$fetch2.stdout',
  '$xfetch.stdout',
  // forms that do not resolve to a step
  '${steps.fetch.outputs.body.items[0]}',
  '${steps.fetch.outputs}',
  '${steps.fetch.stdout}',
  '${steps.fetch.bar}',
  '${steps.fetch.outputs.}',
  '${fetch.items}',
  '${fetch.stdoutx}',
  '${fetch.stdout:}',
  '${fetch.stdout:a}',
  '${fetch.stdout:1:2:3}',
  '${ fetch.stdout }',
  '"$fetch.stdout"',
  '$fetch.stdout,',
  // escaped tokens
  '\\${steps.fetch.outputs.body}',
  '\\${fetch.stdout}',
  '\\$fetch.stdout',
  '\\\\\\${steps.fetch.outputs.body}',
  // unterminated token
  '${steps.fetch.outputs.body',
  '',
];

describe('rewriteStepReferences', () => {
  it.each(rewritten)('rewrites %j', (input, expected) => {
    expect(rewriteStepReferences(input, 'fetch', 'load')).toBe(expected);
  });

  it.each(unchanged)('keeps %j', (input) => {
    expect(rewriteStepReferences(input, 'fetch', 'load')).toBe(input);
  });

  it('keeps the value when the names are equal', () => {
    expect(rewriteStepReferences('${fetch.stdout}', 'fetch', 'fetch')).toBe(
      '${fetch.stdout}'
    );
  });

  it('matches the name exactly', () => {
    expect(rewriteStepReferences('${Fetch.stdout}', 'fetch', 'load')).toBe(
      '${Fetch.stdout}'
    );
    expect(
      rewriteStepReferences(
        '${steps.fetch2.outputs.a} ${fetch.stdout}',
        'fetch2',
        'x'
      )
    ).toBe('${steps.x.outputs.a} ${fetch.stdout}');
  });

  it('keeps a token after an escaped one', () => {
    expect(
      rewriteStepReferences(
        '\\${fetch.stdout} ${fetch.stdout}',
        'fetch',
        'load'
      )
    ).toBe('\\${fetch.stdout} ${load.stdout}');
  });
});

describe('mentionsStep', () => {
  it.each(rewritten)('finds a token in %j', (input) => {
    expect(mentionsStep(input, 'fetch')).toBe(true);
  });

  it.each(unchanged)('finds no token in %j', (input) => {
    expect(mentionsStep(input, 'fetch')).toBe(false);
  });

  it('is false for another step', () => {
    expect(mentionsStep('${steps.fetch.outputs.a}', 'load')).toBe(false);
  });
});

describe('outputReferenceIds', () => {
  it('names the step of each step-output reference', () => {
    expect(
      outputReferenceIds('${steps.fetch.outputs.a} ${ steps.load.outputs.b }')
    ).toEqual(['fetch', 'load']);
  });

  it('skips escaped tokens and other forms', () => {
    expect(
      outputReferenceIds(
        '\\${steps.fetch.outputs.a} ${fetch.stdout} ${params.x} $steps.a'
      )
    ).toEqual([]);
  });
});
