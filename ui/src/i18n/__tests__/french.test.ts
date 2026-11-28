// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { backfillMessages } from '@/features/backfills/messages';
import { workspaceModules } from '@/features/workflow-workspace/messages';
import { workerAdminMessages } from '@/features/workers/messages';
import { inventoryMessages } from '@/pages/nodes/inventories/messages';
import { nodeTestMessages, workerNodeMessages } from '@/pages/nodes/messages';
import { findFrenchTextProblems } from '@/testing/frenchText';
import { messages } from '../messages';
import { baseStaticMessages, staticMessages } from '../staticMessages';
import { translateStatic } from '../translateStatic';

type Catalog = Readonly<Record<string, string>>;

interface MessageModule {
  readonly en: Catalog;
  readonly fr: Catalog;
}

// Every module that feeds the static catalog.
const MODULES: Readonly<Record<string, MessageModule>> = {
  ...workspaceModules,
  backfills: backfillMessages,
  nodes: nodeTestMessages,
  workerNodes: workerNodeMessages,
  inventories: inventoryMessages,
  workerAdmin: workerAdminMessages,
};

function sameKeys(a: Catalog, b: Catalog): boolean {
  const left = Object.keys(a).sort();
  const right = Object.keys(b).sort();
  return (
    left.length === right.length && left.every((key, i) => key === right[i])
  );
}

// Returns one line for each rule that a French catalog breaks; an empty list
// means a valid catalog.
function findCatalogProblems(
  id: string,
  english: Catalog,
  french: Catalog
): string[] {
  const problems = sameKeys(english, french)
    ? []
    : [`${id}: fr keys differ from en keys`];
  for (const [key, value] of Object.entries(french)) {
    problems.push(
      ...findFrenchTextProblems(id, key, value, english[key] ?? key)
    );
  }
  return problems;
}

describe('French catalogs', () => {
  it('write every module text by the French rules', () => {
    const problems = Object.entries(MODULES).flatMap(([id, module]) =>
      findCatalogProblems(id, module.en, module.fr)
    );
    expect(problems).toEqual([]);
  });

  it('write every base text by the French rules', () => {
    expect(
      findCatalogProblems('base', baseStaticMessages.en, baseStaticMessages.fr)
    ).toEqual([]);
  });

  it('write every shell text by the French rules', () => {
    expect(findCatalogProblems('shell', messages.en, messages.fr)).toEqual([]);
  });

  // The static catalog shows the base text of a key that both hold, so a
  // module gives such a key the same French text.
  it('give a module key that the base also holds its base text', () => {
    const base: Catalog = baseStaticMessages.fr;
    const shadowed = Object.entries(MODULES).flatMap(([id, module]) =>
      Object.entries(module.fr)
        .filter(([key, value]) => key in base && base[key] !== value)
        .map(([key]) => `${id}: "${key}"`)
    );
    expect(shadowed).toEqual([]);
  });
});

describe('French static catalog', () => {
  it('holds every module entry, base entries first', () => {
    const catalog: Catalog = staticMessages.fr;
    const base: Catalog = baseStaticMessages.fr;
    for (const module of Object.values(MODULES)) {
      for (const [key, value] of Object.entries(module.fr)) {
        expect(catalog[key]).toBe(base[key] ?? value);
      }
    }
  });

  it('translates backfill text and fills its placeholders', () => {
    expect(translateStatic('fr', 'Backfills')).toBe('Rattrapages');
    expect(translateStatic('fr', 'Cancel the backfill?')).toBe(
      'Annuler le rattrapage\u00a0?'
    );
    expect(
      translateStatic('fr', 'Backfill {done}/{total}, {failed} failed', {
        done: 12,
        total: 28,
        failed: 1,
      })
    ).toBe('Rattrapage 12/28, 1 en échec');
    expect(
      translateStatic('fr', 'Times are in {zone}.', { zone: 'Europe/Paris' })
    ).toBe('Les heures sont exprimées dans le fuseau Europe/Paris.');
  });

  // These keys are also base entries: other pages show the same French.
  it('translates the backfill keys that other pages share', () => {
    expect(translateStatic('fr', 'Open workflow')).toBe('Ouvrir le workflow');
    expect(
      translateStatic('fr', 'Please try again or check the server connection.')
    ).toBe('Réessayez ou vérifiez la connexion au serveur.');
  });
});
