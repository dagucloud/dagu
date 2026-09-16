// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RunDateMode, RunDatePreset, ViewSpecType } from '@/api/v1/schema';
import type { View, ViewSpec } from '@/hooks/useViews';
import type { ViewScope } from '@/features/views/viewScope';

/** ARTIFACT_PRESET_ALL selects every run regardless of date. */
export const ARTIFACT_PRESET_ALL = RunDatePreset.all;

export type ArtifactsDateRangeMode = 'preset' | 'custom';

export type ArtifactsFilterSet = {
  searchText: string;
  fileName: string;
  fromDate?: string;
  toDate?: string;
  dateRangeMode: ArtifactsDateRangeMode;
  datePreset: string;
};

export type ArtifactsFilterView = {
  id: string;
  name: string;
  pinned: boolean;
  filters: ArtifactsFilterSet;
};

export function artifactsFilterSetFromView(view: View): ArtifactsFilterSet {
  return {
    searchText: view.dagName ?? '',
    fileName: view.fileName ?? '',
    fromDate: view.fromDate ?? undefined,
    toDate: view.toDate ?? undefined,
    dateRangeMode: asDateRangeMode(view.dateMode),
    datePreset: view.datePreset ?? ARTIFACT_PRESET_ALL,
  };
}

// The Artifacts page has no period granularity, so anything that is not an
// explicit custom range is treated as a preset.
function asDateRangeMode(value: string | undefined): ArtifactsDateRangeMode {
  return value === RunDateMode.custom ? 'custom' : 'preset';
}

export function buildArtifactViewSpec(
  name: string,
  filters: ArtifactsFilterSet,
  isDefault: boolean,
  pinned: boolean,
  scope: ViewScope
): ViewSpec {
  return {
    name,
    type: ViewSpecType.artifact,
    workspace: scope.workspace,
    workspaceScope: scope.workspaceScope,
    dagName: filters.searchText,
    fileName: filters.fileName,
    dateMode: filters.dateRangeMode as RunDateMode,
    datePreset: filters.datePreset as RunDatePreset,
    // Send the dates explicitly, never omitted: the API preserves a field the
    // request leaves out, so an omitted date would restore the value the user
    // just cleared.
    fromDate: filters.fromDate ?? '',
    toDate: filters.toDate ?? '',
    intervalDays: 1,
    pinned,
    isDefault,
  };
}
