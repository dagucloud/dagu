// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncItemKind } from '@/api/v1/schema';
import { describe, expect, it } from 'vitest';
import {
  createSyncKindCounts,
  deriveSyncKindFromItemId,
  normalizeSyncItemKind,
  parseSyncKind,
  syncKindFilters,
  syncKindLabels,
} from '../sync-kind';

describe('sync-kind', () => {
  it('exposes every kind as a filter with labels and counts', () => {
    expect(syncKindFilters).toEqual(['dag', 'doc', 'doc-asset']);
    for (const kind of syncKindFilters) {
      expect(syncKindLabels[kind].plural).toBeTruthy();
      expect(createSyncKindCounts()[kind]).toBe(0);
    }
  });

  it('parses only known kinds from the URL', () => {
    expect(parseSyncKind('doc-asset')).toBe('doc-asset');
    expect(parseSyncKind('doc')).toBe('doc');
    expect(parseSyncKind('bogus')).toBe('dag');
    expect(parseSyncKind(null)).toBe('dag');
  });

  it('normalizes API kinds including attachments', () => {
    expect(normalizeSyncItemKind(SyncItemKind.dag)).toBe('dag');
    expect(normalizeSyncItemKind(SyncItemKind.doc)).toBe('doc');
    expect(normalizeSyncItemKind(SyncItemKind.doc_asset)).toBe('doc-asset');
  });

  it('derives the kind from item IDs with attachments taking precedence', () => {
    expect(deriveSyncKindFromItemId('docs/.attachments/guides/x/logo.png')).toBe(
      'doc-asset'
    );
    expect(deriveSyncKindFromItemId('docs/guides/x')).toBe('doc');
    expect(deriveSyncKindFromItemId('my-dag')).toBe('dag');
  });
});
