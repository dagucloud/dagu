// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { baseStaticMessages, staticMessages } from '@/i18n/staticMessages';
import { workspaceMessages, workspaceModules } from '..';

type Catalog = Readonly<Record<string, string>>;
type LocaleId = 'en' | 'zh-CN' | 'ja';
type MessageModule = Readonly<Record<LocaleId, Catalog>>;

const LOCALES: readonly LocaleId[] = ['en', 'zh-CN', 'ja'];
const TRANSLATED: readonly LocaleId[] = ['zh-CN', 'ja'];

function hasOwn(catalog: Catalog, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(catalog, key);
}

function sameKeys(a: Catalog, b: Catalog): boolean {
  const left = Object.keys(a).sort();
  const right = Object.keys(b).sort();
  return (
    left.length === right.length && left.every((key, i) => key === right[i])
  );
}

// Returns one line for each broken rule; an empty list means valid modules.
function findProblems(
  modules: Readonly<Record<string, MessageModule>>,
  base: MessageModule
): string[] {
  const problems: string[] = [];
  const firstOwner = new Map<string, string>();

  for (const [id, module] of Object.entries(modules)) {
    for (const locale of TRANSLATED) {
      if (!sameKeys(module.en, module[locale])) {
        problems.push(`${id}: ${locale} keys differ from en keys`);
      }
    }

    for (const locale of LOCALES) {
      for (const [key, value] of Object.entries(module[locale])) {
        if (value.trim() === '') {
          problems.push(`${id}: empty ${locale} value for "${key}"`);
        }
      }
    }

    for (const [key, value] of Object.entries(module.en)) {
      if (value !== key) {
        problems.push(`${id}: en value of "${key}" is not its key`);
      }
      if (hasOwn(base.en, key)) {
        for (const locale of TRANSLATED) {
          if (module[locale][key] !== base[locale][key]) {
            problems.push(`${id}: "${key}" shadowed in ${locale}`);
          }
        }
      }
      const owner = firstOwner.get(key);
      if (owner === undefined) {
        firstOwner.set(key, id);
        continue;
      }
      const other = modules[owner]!;
      for (const locale of LOCALES) {
        if (module[locale][key] !== other[locale][key]) {
          problems.push(
            `${owner}, ${id}: "${key}" has different ${locale} texts`
          );
        }
      }
    }
  }
  return problems;
}

const base: MessageModule = {
  en: { Save: 'Save' },
  'zh-CN': { Save: '保存' },
  ja: { Save: '保存' },
};

function module(
  en: Catalog,
  zh: Catalog = en,
  ja: Catalog = en
): MessageModule {
  return { en, 'zh-CN': zh, ja };
}

describe('workspace message modules', () => {
  it('pass every rule', () => {
    expect(findProblems(workspaceModules, baseStaticMessages)).toEqual([]);
  });

  it('import every module file', () => {
    const dir = path.resolve(
      path.dirname(fileURLToPath(import.meta.url)),
      '..'
    );
    const files = readdirSync(dir)
      .filter((file) => file.endsWith('.ts'))
      .map((file) => file.replace(/\.ts$/, ''))
      .filter((name) => name !== 'define' && name !== 'index')
      .sort();
    expect(Object.keys(workspaceModules).sort()).toEqual(files);
  });

  it('give no module to a wave 1 task', () => {
    for (const id of ['t01', 't02', 't03a', 't03b', 't04', 't05', 'n1']) {
      expect(workspaceModules).not.toHaveProperty(id);
    }
  });

  it('merge into the static catalog with base entries first', () => {
    for (const locale of LOCALES) {
      const catalog: Catalog = staticMessages[locale];
      const merged: Catalog = workspaceMessages[locale];
      const baseCatalog: Catalog = baseStaticMessages[locale];
      for (const key of Object.keys(merged)) {
        const expected = hasOwn(baseCatalog, key)
          ? baseCatalog[key]
          : merged[key];
        expect(catalog[key]).toBe(expected);
      }
      for (const key of Object.keys(baseCatalog)) {
        expect(catalog[key]).toBe(baseCatalog[key]);
      }
    }
  });

  it('accept a valid module that reuses a base key', () => {
    const modules = {
      t08: module(
        { Save: 'Save', 'Run…': 'Run…' },
        { Save: '保存', 'Run…': '运行…' },
        { Save: '保存', 'Run…': '実行…' }
      ),
      t09: module({ 'Run…': 'Run…' }, { 'Run…': '运行…' }, { 'Run…': '実行…' }),
    };
    expect(findProblems(modules, base)).toEqual([]);
  });

  it('reject an en value that is not its key', () => {
    const modules = { t08: module({ Run: 'Start' }) };
    expect(findProblems(modules, base)).toEqual([
      't08: en value of "Run" is not its key',
    ]);
  });

  it('reject locales with different key sets', () => {
    const modules = {
      t08: module({ Run: 'Run' }, { Run: '运行', Stop: '停止' }, {}),
    };
    expect(findProblems(modules, base)).toEqual([
      't08: zh-CN keys differ from en keys',
      't08: ja keys differ from en keys',
    ]);
  });

  it('reject a module that shadows a base translation', () => {
    const modules = {
      t08: module({ Save: 'Save' }, { Save: '存储' }, { Save: '保存' }),
    };
    expect(findProblems(modules, base)).toEqual([
      't08: "Save" shadowed in zh-CN',
    ]);
  });

  it('reject a key with different texts in two modules', () => {
    const modules = {
      t08: module({ Run: 'Run' }, { Run: '运行' }, { Run: '実行' }),
      t09: module({ Run: 'Run' }, { Run: '执行' }, { Run: '実行' }),
    };
    expect(findProblems(modules, base)).toEqual([
      't08, t09: "Run" has different zh-CN texts',
    ]);
  });

  it('reject an empty value', () => {
    const modules = {
      t08: module({ Run: 'Run' }, { Run: ' ' }, { Run: '実行' }),
    };
    expect(findProblems(modules, base)).toEqual([
      't08: empty zh-CN value for "Run"',
    ]);
  });
});
