// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncItemKind } from '@/api/v1/schema';

export type SyncKind = 'dag' | 'doc';

export const syncKindFilters: SyncKind[] = ['dag', 'doc'];

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
};

export const syncKindBadgeClass: Partial<Record<SyncKind, string>> = {
  doc: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400',
};

export function createSyncKindCounts(): Record<SyncKind, number> {
  return {
    dag: 0,
    doc: 0,
  };
}

export function parseSyncKind(value: string | null): SyncKind {
  if (value && syncKindFilters.includes(value as SyncKind)) {
    return value as SyncKind;
  }
  return 'dag';
}

export function normalizeSyncItemKind(kind: SyncItemKind): SyncKind {
  return kind === SyncItemKind.doc ? 'doc' : 'dag';
}

export function deriveSyncKindFromItemId(id: string): SyncKind {
  if (id.startsWith('docs/')) return 'doc';
  return 'dag';
}
