// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import type { NoticeLineItem } from '@/features/dags/components/dag-editor/NoticeLine';
import { setSkillKey } from './skillForm';

export type SkillSummary = components['schemas']['SkillSummary'];

/** The skills tab of the AI page. It lists the skills and opens the selected one. */
export const SKILLS_PATH = '/ai/skills';

export type NameProblem = 'pattern' | 'length' | 'reserved' | 'exists';

/** The last path segment of the address that opens the new-skill dialog. */
export const NEW_SKILL_SEGMENT = 'new';

/** The address that selects one skill. */
export function skillPath(name: string): string {
  return `${SKILLS_PATH}/${encodeURIComponent(name)}`;
}

/** The address that opens the new-skill dialog. */
export const NEW_SKILL_PATH = `${SKILLS_PATH}/${NEW_SKILL_SEGMENT}`;

/** The longest name that the Agent Skills format accepts. */
export const SKILL_NAME_MAX = 64;

/**
 * The name rule of the Agent Skills format: lowercase letters and digits in
 * groups that single hyphens join. A digit may come first. The name is the
 * folder `skills/<name>/`.
 */
export const SKILL_NAME_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/**
 * Checks the name of a new skill, with 'new' reserved for its address.
 * The server refuses a name that exists, and the check here says so first.
 */
export function skillNameProblem(
  name: string,
  existing: readonly string[] = []
): NameProblem | null {
  if (name === '') return null;
  if (name.length > SKILL_NAME_MAX) return 'length';
  if (!SKILL_NAME_PATTERN.test(name)) return 'pattern';
  if (name === NEW_SKILL_SEGMENT) return 'reserved';
  if (existing.includes(name)) return 'exists';
  return null;
}

/**
 * The text of a new SKILL.md. The name is empty until newSkillText sets it.
 * It follows the format: the description has more than 20 characters and
 * says when to use the skill.
 */
export const SKILL_TEMPLATE =
  '---\nname:\ndescription: Say what this skill does. Use when the task needs these instructions.\n---\n# Instructions\n\nWrite the steps that the model follows here.\n';

/** The text of a new SKILL.md for a name that skillNameProblem accepts. */
export function newSkillText(name: string): string {
  const outcome = setSkillKey(SKILL_TEMPLATE, 'name', name);
  return outcome.ok ? outcome.text : SKILL_TEMPLATE;
}

/** Keeps the skills whose name or description has the query. */
export function filterSkills<
  T extends Pick<SkillSummary, 'name' | 'description'>,
>(skills: readonly T[], query: string): T[] {
  const needle = query.trim().toLowerCase();
  if (needle === '') return [...skills];
  return skills.filter(
    (skill) =>
      skill.name.toLowerCase().includes(needle) ||
      skill.description.toLowerCase().includes(needle)
  );
}

/** The `with` of a chat step or an agent step that offers the skill. */
export function stepSnippet(name: string): string {
  return `with:\n  skills: [${name}]\n`;
}

/**
 * The notices of the editor panel of a skill: the message of a failed save.
 * The errors and the warnings of the format have their own panel.
 */
export function editorNotices({
  saveError,
}: {
  saveError: string | null;
}): NoticeLineItem[] {
  const notices: NoticeLineItem[] = [];
  if (saveError) {
    notices.push({
      id: 'save-error',
      tone: 'error',
      priority: 2,
      role: 'alert',
      message: saveError,
    });
  }
  return notices;
}
