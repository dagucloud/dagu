// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isRecord } from '../record';

/** One item of `with.skills`: a skill name. */
export interface SkillEntryModel {
  /** Position in the `skills` list. */
  index: number;
  /** The skill name. Empty for an item that is not a name. */
  name: string;
  /** Set on an item that is not a name: what is wrong. */
  problem?: string;
}

/** Keys of `with` that the Skills section owns. */
export const SKILL_WITH_KEYS = ['skills'] as const;

function readSkill(raw: unknown, index: number): SkillEntryModel {
  if (typeof raw === 'string') return { index, name: raw.trim() };
  return { index, name: '', problem: 'A skill entry is a name.' };
}

/**
 * Reads `with.skills` from the plain value of the step entry. It never
 * throws: a value of the wrong type gives no entry.
 */
export function readStepSkills(
  step: Record<string, unknown>
): SkillEntryModel[] {
  const withBlock = step.with;
  if (!isRecord(withBlock) || !Array.isArray(withBlock.skills)) return [];
  return withBlock.skills.map(readSkill);
}
