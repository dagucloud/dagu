// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import tailwindcss from '@tailwindcss/postcss';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const semanticUtilities = [
  'bg-error/10',
  'bg-error-muted',
  'bg-info-muted',
  'bg-success-muted',
  'bg-surface-variant/60',
  'bg-warning-muted',
  'border-error/30',
  'hover:bg-primary-hover',
  'text-error',
  'text-text-secondary',
];

function selector(utility: string): string {
  return `.${utility.replace(/:/g, '\\:').replace(/\//g, '\\/')}`;
}

describe('global styles', () => {
  it('generates used semantic color utilities', async () => {
    const file = resolve('src/styles/global.css');
    const source = await readFile(file, 'utf8');
    const result = await postcss([tailwindcss()]).process(source, {
      from: file,
    });

    for (const utility of semanticUtilities) {
      expect(result.css).toContain(selector(utility));
    }
  });
});
