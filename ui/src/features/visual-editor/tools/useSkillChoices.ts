// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { skillState, skillUnusableReason } from '@/features/skills/skillIssues';
import type { SkillSummary } from '@/features/skills/skillIssues';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

export interface SkillChoice {
  name: string;
  description: string;
  /**
   * False when the server refuses the skill (it has an error): a step that
   * names it fails. A picker shows the skill, disabled, with `reason`.
   */
  usable: boolean;
  /** The first error of an unusable skill. Empty for a usable skill. */
  reason: string;
}

/** The choices of a list of skills: a skill with an error stays, unusable. */
export function skillChoices(skills: readonly SkillSummary[]): SkillChoice[] {
  return skills.map((skill) => {
    const usable = skillState(skill) !== 'error';
    return {
      name: skill.name,
      description: skill.description,
      usable,
      reason: usable ? '' : (skillUnusableReason(skill) ?? ''),
    };
  });
}

/**
 * The skills of the skills directory, for the dock and the Skills section
 * that add a skill, and for the descriptions that the canvas shows.
 * `available` is false until the list has arrived, and stays false when the
 * request fails; the dock then keeps its free-text field. Nothing is
 * requested while `enabled` is false. Every caller shares one request.
 */
export function useSkillChoices(enabled = true): {
  skills: SkillChoice[];
  available: boolean;
} {
  const remoteNode = useRemoteNode();
  const { data } = useQuery(
    '/skills',
    whenEnabled(enabled, { params: { query: { remoteNode } } })
  );
  const skills = useMemo(
    () => skillChoices(data?.skills ?? []),
    [data?.skills]
  );
  return { skills, available: enabled && data !== undefined };
}
