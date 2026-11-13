// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import tailwindcss from '@tailwindcss/postcss';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import postcss, { type Root, type Rule } from 'postcss';
import { describe, expect, it } from 'vitest';
import { EXPORT_STATUS_COLORS } from '@/features/visual-editor/canvas/exportCanvas';

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
  ['border-node-line', 'border-color', '--node-line'],
  ['stroke-node-line', 'stroke', '--node-line'],
] as const;

function selector(utility: string): string {
  return `.${utility.replace(/([:/[\]=])/g, '\\$1')}`;
}

async function compileGlobalStyles(): Promise<Root> {
  const file = resolve('src/styles/global.css');
  const source = await readFile(file, 'utf8');
  const result = await postcss([tailwindcss()]).process(source, {
    from: file,
  });
  return result.root;
}

// Layers rank by first declaration across all loaded stylesheets.
function layerOrder(sheets: Root[]): string[] {
  const order: string[] = [];
  for (const sheet of sheets) {
    sheet.walkAtRules('layer', (rule) => {
      for (const name of rule.params.split(',').map((part) => part.trim())) {
        if (name && !order.includes(name)) {
          order.push(name);
        }
      }
    });
  }
  return order;
}

// WCAG AA for normal text.
const MIN_TEXT_CONTRAST = 4.5;

/** The `#rrggbb` value of each custom property of a theme rule. */
async function themeColors(
  theme: ':root' | '.dark'
): Promise<Map<string, string>> {
  const source = await readFile(resolve('src/styles/global.css'), 'utf8');
  const colors = new Map<string, string>();
  postcss.parse(source).walkRules((rule) => {
    if (rule.selector !== theme) return;
    rule.walkDecls((decl) => {
      if (/^#[0-9a-f]{6}$/i.test(decl.value)) colors.set(decl.prop, decl.value);
    });
  });
  return colors;
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((at) => {
    const channel = parseInt(hex.slice(at, at + 2), 16) / 255;
    return channel <= 0.03928
      ? channel / 12.92
      : ((channel + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r! + 0.7152 * g! + 0.0722 * b!;
}

function contrast(a: string, b: string): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light! + 0.05) / (dark! + 0.05);
}

describe('global styles', () => {
  it('generates utilities from semantic tokens', async () => {
    const root = await compileGlobalStyles();

    for (const [utility, property, token] of semanticUtilities) {
      let rule: Rule | undefined;
      root.walkRules((candidate) => {
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

  // The API reference stylesheet loads after the app's and resets Tailwind's
  // --tw-* variables on every element. Its layers must rank below the app's,
  // or translate, shadow and ring utilities stop working app-wide.
  it('ranks API reference layers below app layers', async () => {
    const scalarFile = createRequire(import.meta.url).resolve(
      '@scalar/api-reference-react/style.css'
    );
    const scalar = postcss.parse(await readFile(scalarFile, 'utf8'));
    const scalarLayers = layerOrder([scalar]);
    const order = layerOrder([await compileGlobalStyles(), scalar]);

    expect(scalarLayers).not.toHaveLength(0);
    for (const layer of scalarLayers) {
      expect(order.indexOf(layer), layer).toBeLessThan(order.indexOf('theme'));
    }
  });
  // The error and warning counts of a step card are text on these fills.
  it.each([':root', '.dark'] as const)(
    'keeps the text of an issue count readable in %s',
    async (theme) => {
      const colors = await themeColors(theme);
      for (const fill of ['--destructive', '--warning']) {
        const ratio = contrast(
          colors.get(fill)!,
          colors.get(`${fill}-foreground`)!
        );
        expect(ratio, fill).toBeGreaterThanOrEqual(MIN_TEXT_CONTRAST);
      }
    }
  );
});

// The tokens that the graph canvas, its nodes and its chips use. Each one has
// a value in both themes, so that no meaning falls back to a literal colour.
const graphTokens = [
  '--background',
  '--card',
  '--border',
  '--node-line',
  '--muted',
  '--muted-foreground',
  '--primary',
  '--destructive',
  '--success',
  '--warning',
  '--warning-foreground',
  '--status-success',
  '--status-error',
  '--status-running',
  '--status-warning',
  '--status-aborted',
  '--status-neutral',
] as const;

async function themeTokens(selector: string): Promise<Map<string, string>> {
  const source = await readFile(resolve('src/styles/global.css'), 'utf8');
  const root = postcss.parse(source);
  const tokens = new Map<string, string>();
  root.walkRules((rule) => {
    if (rule.selector !== selector) return;
    rule.walkDecls(/^--/, (declaration) => {
      tokens.set(declaration.prop, declaration.value.trim());
    });
  });
  return tokens;
}

describe('graph colour tokens', () => {
  it.each([':root', '.dark'])('defines every token in %s', async (selector) => {
    const tokens = await themeTokens(selector);
    for (const name of graphTokens) {
      expect(tokens.has(name), `${name} in ${selector}`).toBe(true);
    }
  });

  // The node line is a UI boundary: 3:1 on the canvas. The text of a warning
  // count badge is 4.5:1 on the warning colour.
  it.each([':root', '.dark'])(
    'keeps the contrast of the graph in %s',
    async (selector) => {
      const tokens = await themeTokens(selector);
      const value = (name: string) => tokens.get(name)!;
      expect(
        contrast(value('--node-line'), value('--background'))
      ).toBeGreaterThanOrEqual(3);
      expect(
        contrast(value('--node-line'), value('--card'))
      ).toBeGreaterThanOrEqual(3);
      for (const status of [
        '--status-success',
        '--status-error',
        '--status-running',
        '--status-warning',
        '--status-aborted',
      ]) {
        expect(
          contrast(value(status), value('--card')),
          `${status} on the card`
        ).toBeGreaterThanOrEqual(3);
      }
      if (selector === ':root') {
        expect(
          contrast(value('--warning-foreground'), value('--warning'))
        ).toBeGreaterThanOrEqual(4.5);
      }
    }
  );

  // A running step is violet and an aborted step is not: the two never share a hue.
  it('keeps the aborted colour apart from the running colour', async () => {
    for (const selector of [':root', '.dark']) {
      const tokens = await themeTokens(selector);
      expect(tokens.get('--status-aborted')).not.toBe(
        tokens.get('--status-running')
      );
    }
  });

  // One palette on screen and in the export: each --graph-* token equals the
  // --status-* token of its theme, and the literals of the export equal both.
  it.each([
    [':root', 'light'],
    ['.dark', 'dark'],
  ] as const)(
    'keeps the graph palette in step in %s',
    async (selector, mode) => {
      const tokens = await themeTokens(selector);
      const exported = EXPORT_STATUS_COLORS[mode];
      const rows = [
        ['success', exported.success],
        ['error', exported.failed],
        ['running', exported.running],
        ['warning', exported.warning],
        ['aborted', exported.aborted],
        ['neutral', exported.neutral],
      ] as const;
      for (const [tone, color] of rows) {
        expect(tokens.get(`--graph-${tone}`), `--graph-${tone}`).toBe(color);
        expect(tokens.get(`--status-${tone}`), `--status-${tone}`).toBe(color);
      }
    }
  );
});
