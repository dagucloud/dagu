// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { RunDateMode, ViewWorkspaceScope } from '@/api/v1/schema';
import {
  ARTIFACT_PRESET_ALL,
  buildArtifactViewSpec,
  type ArtifactsFilterSet,
} from '../artifactViews';

const scope = {
  workspace: '',
  workspaceScope: ViewWorkspaceScope.all,
};

function filters(
  overrides: Partial<ArtifactsFilterSet> = {}
): ArtifactsFilterSet {
  return {
    searchText: 'nightly-etl',
    fileName: '**/*.csv',
    fromDate: '2026-09-01T00:00',
    toDate: '2026-09-30T23:59',
    dateRangeMode: 'custom',
    datePreset: ARTIFACT_PRESET_ALL,
    ...overrides,
  };
}

describe('buildArtifactViewSpec', () => {
  // The API preserves fields a request omits, so a cleared date must be sent
  // as an explicit empty value rather than dropped from the payload.
  it('sends cleared dates explicitly instead of omitting them', () => {
    const spec = buildArtifactViewSpec(
      'Nightly reports',
      filters({ toDate: undefined }),
      false,
      false,
      scope
    );

    expect(spec.toDate).toBe('');
    expect('toDate' in spec).toBe(true);
    expect(JSON.parse(JSON.stringify(spec))).toHaveProperty('toDate', '');
  });

  it('carries the filter set onto the spec', () => {
    const spec = buildArtifactViewSpec(
      'Nightly reports',
      filters(),
      true,
      true,
      scope
    );

    expect(spec).toMatchObject({
      name: 'Nightly reports',
      dagName: 'nightly-etl',
      fileName: '**/*.csv',
      dateMode: RunDateMode.custom,
      fromDate: '2026-09-01T00:00',
      toDate: '2026-09-30T23:59',
      pinned: true,
      isDefault: true,
    });
  });
});
