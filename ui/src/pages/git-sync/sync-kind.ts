// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncItemKind } from '@/api/v1/schema';

export type SyncKind = 'dag' | 'doc' | 'config' | 'memory' | 'skill' | 'soul';

export const syncKindFilters: SyncKind[] = [
  'dag',
  'doc',
  'config',
  'memory',
  'skill',
  'soul',
];

export const syncKindLabels: Record<
  SyncKind,
  {
    singular: string;
    plural: string;
    selectionSingular: string;
    selectionPlural: string;
    badge: string;
  }
> = {
  dag: {
    singular: 'DAG',
    plural: 'DAGs',
    selectionSingular: 'DAG',
    selectionPlural: 'DAGs',
    badge: 'dag',
  },
  doc: {
    singular: 'doc',
    plural: 'Docs',
    selectionSingular: 'doc',
    selectionPlural: 'docs',
    badge: 'doc',
  },
  config: {
    singular: 'config',
    plural: 'Config',
    selectionSingular: 'config',
    selectionPlural: 'config',
    badge: 'config',
  },
  memory: {
    singular: 'memory',
    plural: 'Memory',
    selectionSingular: 'memory',
    selectionPlural: 'memory',
    badge: 'memory',
  },
  skill: {
    singular: 'skill',
    plural: 'Skills',
    selectionSingular: 'skill',
    selectionPlural: 'skills',
    badge: 'skill',
  },
  soul: {
    singular: 'soul',
    plural: 'Souls',
    selectionSingular: 'soul',
    selectionPlural: 'souls',
    badge: 'soul',
  },
};

export const syncKindBadgeClass: Partial<Record<SyncKind, string>> = {
  doc: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
  memory: 'bg-purple-500/10 text-purple-600 dark:text-purple-400',
  skill: 'bg-pink-500/10 text-pink-600 dark:text-pink-400',
  soul: 'bg-cyan-500/10 text-cyan-600 dark:text-cyan-400',
};

export function createSyncKindCounts(): Record<SyncKind, number> {
  return {
    dag: 0,
    doc: 0,
    config: 0,
    memory: 0,
    skill: 0,
    soul: 0,
  };
}

export function parseSyncKind(value: string | null): SyncKind {
  if (value && syncKindFilters.includes(value as SyncKind)) {
    return value as SyncKind;
  }
  return 'dag';
}

export function normalizeSyncItemKind(kind: SyncItemKind): SyncKind {
  switch (kind) {
    case SyncItemKind.doc:
      return 'doc';
    case SyncItemKind.config:
      return 'config';
    case SyncItemKind.memory:
      return 'memory';
    case SyncItemKind.skill:
      return 'skill';
    case SyncItemKind.soul:
      return 'soul';
    default:
      return 'dag';
  }
}

export function deriveSyncKindFromItemId(id: string): SyncKind {
  if (id === 'base' || /^workspaces\/[^/]+\/base$/.test(id)) return 'config';
  if (id.startsWith('docs/')) return 'doc';
  if (id.startsWith('memory/')) return 'memory';
  if (id.startsWith('skills/')) return 'skill';
  if (id.startsWith('souls/')) return 'soul';
  return 'dag';
}
