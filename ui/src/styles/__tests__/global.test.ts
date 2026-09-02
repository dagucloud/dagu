// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import tailwindcss from '@tailwindcss/postcss';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import postcss, { type Rule } from 'postcss';
import { describe, expect, it } from 'vitest';

const semanticUtilities = [
  ['bg-error/10', 'background-color', '--status-error'],
  ['bg-error-muted', 'background-color', '--error-muted'],
  ['bg-info-muted', 'background-color', '--info-muted'],
  ['bg-muted', 'background-color', '--muted'],
  ['bg-success-muted', 'background-color', '--success-muted'],
  ['bg-surface', 'background-color', '--surface'],
  ['bg-surface-variant/60', 'background-color', '--surface-variant'],
  ['bg-warning-muted', 'background-color', '--warning-muted'],
  ['border-error/30', 'border-color', '--status-error'],
  ['data-[state=checked]:bg-foreground', 'background-color', '--foreground'],
  ['data-[state=checked]:text-background', 'color', '--background'],
  ['hover:bg-primary-hover', 'background-color', '--primary-hover'],
  ['text-error', 'color', '--status-error'],
  ['text-muted-foreground', 'color', '--muted-foreground'],
  ['text-text-secondary', 'color', '--text-secondary'],
  ['bg-sidebar-active', 'background-color', '--sidebar-active'],
  ['bg-sidebar-primary', 'background-color', '--sidebar-primary'],
] as const;

function selector(utility: string): string {
  return `.${utility.replace(/([:/[\]=])/g, '\\$1')}`;
}

describe('global styles', () => {
  it('generates utilities from semantic tokens', async () => {
    const file = resolve('src/styles/global.css');
    const source = await readFile(file, 'utf8');
    const result = await postcss([tailwindcss()]).process(source, {
      from: file,
    });

    for (const [utility, property, token] of semanticUtilities) {
      let rule: Rule | undefined;
      result.root.walkRules((candidate) => {
        if (candidate.selector === selector(utility)) {
          rule = candidate;
        }
      });

      expect(rule, `${utility} should be generated`).toBeDefined();

      const values: string[] = [];
      rule?.walkDecls(property, (declaration) => {
        values.push(declaration.value);
      });
      expect(
        values.some((value) => value.includes(`var(${token})`)),
        `${utility} should use ${token}`
      ).toBe(true);
    }
  });
});
