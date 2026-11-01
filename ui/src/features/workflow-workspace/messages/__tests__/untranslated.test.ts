// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { baseStaticMessages } from '@/i18n/staticMessages';
import { workspaceModules } from '..';

type Catalog = Readonly<Record<string, string>>;
type LocaleId = 'en' | 'zh-CN' | 'ja';
type MessageModule = Readonly<Record<LocaleId, Catalog>>;

const TRANSLATED = ['zh-CN', 'ja'] as const;

/** Words that every locale writes as in English. */
const TECHNICAL_WORDS = [
  'YAML',
  'JSON',
  'ID',
  'URL',
  'SSE',
  'stdout',
  'stderr',
  'cron',
  'HTTP',
  'SQL',
  'ZIP',
  'PNG',
  'SVG',
] as const;

const TECHNICAL = new RegExp(`\\b(?:${TECHNICAL_WORDS.join('|')})\\b`, 'g');
const LETTER = /\p{L}/u;

// Placeholders such as {count} are not text that a reader sees.
function onlyTechnical(text: string): boolean {
  return !LETTER.test(text.replace(/\{\w+\}/g, ' ').replace(TECHNICAL, ' '));
}

// Returns one line for each value that is the English text.
function findUntranslated(
  modules: Readonly<Record<string, MessageModule>>,
  base: MessageModule
): string[] {
  const problems: string[] = [];
  for (const [id, module] of Object.entries(modules)) {
    for (const key of Object.keys(module.en)) {
      for (const locale of TRANSLATED) {
        const value = module[locale][key];
        if (value === undefined || value !== module.en[key]) continue;
        if (onlyTechnical(value)) continue;
        if (base[locale][key] === value) continue;
        problems.push(`${id}: "${key}" is not translated in ${locale}`);
      }
    }
  }
  return problems;
}

const base: MessageModule = {
  en: { DAG: 'DAG', Save: 'Save' },
  'zh-CN': { DAG: 'DAG', Save: '保存' },
  ja: { DAG: 'DAG', Save: '保存' },
};

describe('workspace message translations', () => {
  // parity:crit-i18n-locales
  it('translate every module key in zh-CN and ja', () => {
    expect(findUntranslated(workspaceModules, baseStaticMessages)).toEqual([]);
  });

  it('report a value that is the English text', () => {
    const modules = {
      t08: {
        en: { 'Run now': 'Run now' },
        'zh-CN': { 'Run now': 'Run now' },
        ja: { 'Run now': '今すぐ実行' },
      },
    };
    expect(findUntranslated(modules, base)).toEqual([
      't08: "Run now" is not translated in zh-CN',
    ]);
  });

  it('accept technical words and base translations', () => {
    const modules = {
      t08: {
        en: { YAML: 'YAML', 'stdout / stderr': 'stdout / stderr', DAG: 'DAG' },
        'zh-CN': {
          YAML: 'YAML',
          'stdout / stderr': 'stdout / stderr',
          DAG: 'DAG',
        },
        ja: { YAML: 'YAML', 'stdout / stderr': 'stdout / stderr', DAG: 'DAG' },
      },
    };
    expect(findUntranslated(modules, base)).toEqual([]);
  });

  it('report a technical word next to other English text', () => {
    const modules = {
      t08: {
        en: { 'Show YAML': 'Show YAML' },
        'zh-CN': { 'Show YAML': '显示 YAML' },
        ja: { 'Show YAML': 'Show YAML' },
      },
    };
    expect(findUntranslated(modules, base)).toEqual([
      't08: "Show YAML" is not translated in ja',
    ]);
  });
});
