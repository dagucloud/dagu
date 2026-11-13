// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BookOpen, Plus, Trash2 } from 'lucide-react';
import { useMemo, useState, type ReactElement } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { Input } from '@/components/ui/input';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { SkillsChange } from '../../../tools/edit';
import { isAgentRunValue } from '../../../agentRun/agentRun';
import { OpenTextLink } from '../../../tools/OpenLink';
import { readStepSkills } from '../../../tools/skills';
import { skillLink } from '../../../tools/targets';
import { useSkillChoices } from '../../../tools/useSkillChoices';
import { FieldIssues } from '../../FieldIssues';
import { issuesUnder } from '../../issues';
import type { SectionProps } from '../../types';
import { SELECT_CLASS } from '../RunEditor';

/**
 * Skills of a `chat.completion` or `agent.run` step: the names of `with.skills` in order.
 * Each has its name, the description of the skill, a link to the page of the
 * skill and a remove button. A select adds a known skill that the list does
 * not have yet. Every change is an `editStepSkills` operation on the YAML,
 * which stays the one source.
 */
export function SkillsSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
}: SectionProps): ReactElement {
  const { ts } = useI18n();
  const entries = useMemo(() => readStepSkills(step.value), [step.value]);
  const { skills: known, available } = useSkillChoices();
  const [typed, setTyped] = useState('');
  const send = (change: SkillsChange) =>
    onEdit({ kind: 'editStepSkills', step: step.key, change });
  const listed = new Set(entries.map((entry) => entry.name));
  // A skill with an error stays in the list, disabled: a step cannot use it.
  const addable = known.filter((skill) => !listed.has(skill.name));
  const typedName = typed.trim();
  return (
    <div className="space-y-2" data-field-path="with.skills" data-reveal-self>
      {entries.length === 0 && (
        <p className="text-xs text-muted-foreground">
          <I18nText text="No skills yet" />
        </p>
      )}
      <ol className="space-y-1">
        {entries.map((entry, index) => {
          const label =
            entry.name === '' ? ts('Invalid skill entry') : entry.name;
          const choice = known.find((skill) => skill.name === entry.name);
          const description = choice?.description;
          const page = skillLink(entry.name);
          return (
            <li
              key={index}
              data-testid="ve-skill-entry"
              data-field-path={`with.skills[${index}]`}
              className="rounded border border-border"
            >
              <div className="flex items-center gap-1 px-1 py-0.5">
                <BookOpen
                  className="ml-1 size-3.5 shrink-0 text-muted-foreground"
                  aria-hidden
                />
                <span className="min-w-0 flex-1 truncate text-xs font-medium">
                  {label}
                </span>
                {!readOnly && (
                  <IconButton
                    size="sm"
                    tone="destructive"
                    label={ts('Remove skill {name}', { name: label })}
                    icon={Trash2}
                    onClick={() => send({ kind: 'remove', index })}
                  />
                )}
              </div>
              {description && (
                <p className="px-2 pb-0.5 text-[11px] text-muted-foreground">
                  {description}
                </p>
              )}
              {choice && !choice.usable && (
                <p className="px-2 pb-0.5 text-xs text-destructive">
                  {ts('The server refuses this skill: {reason}', {
                    reason: choice.reason || ts('It has an error.'),
                  })}
                </p>
              )}
              {entry.problem && (
                <p className="px-2 pb-0.5 text-xs text-destructive">
                  {entry.problem}
                </p>
              )}
              {page && (
                <div className="px-2 pb-1">
                  <OpenTextLink page={page}>
                    <I18nText text="Open the skill" />
                  </OpenTextLink>
                </div>
              )}
              <div className="px-1">
                <FieldIssues
                  issues={issuesUnder(fieldIssues, `with.skills[${index}]`)}
                />
              </div>
            </li>
          );
        })}
      </ol>
      <FieldIssues issues={fieldIssues['with.skills']} />
      {!readOnly &&
        (available ? (
          addable.length > 0 && (
            <select
              data-testid="ve-skill-add"
              data-reveal-focus
              className={SELECT_CLASS}
              aria-label={ts('Add a skill')}
              value=""
              onChange={(event) => {
                if (event.target.value === '') return;
                send({ kind: 'add', name: event.target.value });
              }}
            >
              <option value="">{ts('Add a skill')}</option>
              {addable.map((skill) => (
                <option
                  key={skill.name}
                  value={skill.name}
                  disabled={!skill.usable}
                  title={skill.usable ? undefined : skill.reason}
                >
                  {skill.usable ? skill.name : `${skill.name} (${ts('Error')})`}
                </option>
              ))}
            </select>
          )
        ) : (
          <div className="flex items-center gap-1" data-testid="ve-skill-type">
            <Input
              className="h-7 flex-1 px-2 text-xs"
              aria-label={ts('Skill name')}
              data-reveal-focus
              placeholder={ts('Skill name')}
              value={typed}
              onChange={(event) => setTyped(event.target.value)}
            />
            <IconButton
              size="sm"
              label={ts('Add a skill')}
              icon={Plus}
              disabled={typedName === '' || listed.has(typedName)}
              onClick={() => {
                send({ kind: 'add', name: typedName });
                setTyped('');
              }}
            />
          </div>
        ))}
      <p className="text-[11px] text-muted-foreground">
        <I18nText
          text={
            isAgentRunValue(step.value, step.action)
              ? 'The CLI reads a skill with its tool Skill.'
              : 'The model reads a skill with the tool load_skill.'
          }
        />
      </p>
    </div>
  );
}
