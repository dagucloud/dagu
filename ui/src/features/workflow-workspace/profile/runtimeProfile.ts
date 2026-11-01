// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RuntimeProfileEntryKind, type components } from '@/api/v1/schema';

type Entry = components['schemas']['RuntimeProfileEntryResponse'];

/** A layer of the environment of a run, from the weakest to the strongest. */
export type ProfileLayerKind = 'global' | 'workspace' | 'profile';

export interface ProfileLayer {
  kind: ProfileLayerKind;
  entries: readonly Entry[];
}

/** One key of the merged environment, with the layer that gives its value. */
export interface EffectiveEntry {
  key: string;
  kind: Entry['kind'];
  /** Absent for a secret: the API never returns its value. */
  value: string | undefined;
  layer: ProfileLayerKind;
  /** The weaker layers that define the same key and lose. */
  overrides: ProfileLayerKind[];
}

/**
 * The merge of the server: layers apply in the given order and the later
 * layer wins for a key. The result is sorted by key.
 */
export function mergeProfileLayers(
  layers: readonly ProfileLayer[]
): EffectiveEntry[] {
  const merged = new Map<string, EffectiveEntry>();
  for (const { kind, entries } of layers) {
    for (const entry of entries) {
      const before = merged.get(entry.key);
      merged.set(entry.key, {
        key: entry.key,
        kind: entry.kind,
        value:
          entry.kind === RuntimeProfileEntryKind.secret
            ? undefined
            : entry.value,
        layer: kind,
        overrides: before ? [...before.overrides, before.layer] : [],
      });
    }
  }
  return [...merged.values()].sort((a, b) => a.key.localeCompare(b.key));
}

export type ProfileSelectionSource = 'workflow' | 'workspace' | 'none';

export interface ProfileSelection {
  name: string;
  source: ProfileSelectionSource;
}

/**
 * The profile that a run without an explicit choice uses: the default of the
 * workflow, else the default of its workspace.
 */
export function selectProfile(
  workflowDefault: string | undefined,
  workspaceDefault: string | undefined
): ProfileSelection {
  if (workflowDefault) return { name: workflowDefault, source: 'workflow' };
  if (workspaceDefault) return { name: workspaceDefault, source: 'workspace' };
  return { name: '', source: 'none' };
}
