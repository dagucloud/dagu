// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BookOpen, Plus, Trash2 } from 'lucide-react';
import {
  Fragment,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
} from 'react';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import { isAgentRunValue } from '../agentRun/agentRun';
import type { DagEditOp, StepView, ValidationIssue } from '../contracts';
import { FieldIssues } from '../inspector/FieldIssues';
import { issuesUnder } from '../inspector/issues';
import { matchesWords, queryWords } from '../tools/dockModel';
import { OpenTextLink } from '../tools/OpenLink';
import { readStepSkills } from '../tools/skills';
import { skillLink } from '../tools/targets';
import { useSkillChoices, type SkillChoice } from '../tools/useSkillChoices';
import {
  Chip,
  DetailHeading,
  DockSearch,
  GroupLabel,
  IssueBadge,
} from './DockParts';

export interface SkillsDockProps {
  step: StepView;
  stepLabel: string;
  fieldIssues: Record<string, ValidationIssue[]>;
  locked: boolean;
  /** The dock opened from an Add button: the available skills come first. */
  adding: boolean;
  onEdit(op: DagEditOp): void;
  onClose(): void;
}

/** One row of the Skills dock. */
interface SkillRow {
  id: string;
  group: 'step' | 'catalog' | 'typed';
  name: string;
  description: string;
  /** The place in `with.skills`, for a row of the step. */
  index?: number;
  /** What the server says about the skill, when it knows it. */
  choice?: SkillChoice | undefined;
  /** The entry is not a skill name. */
  problem?: string | undefined;
  issues: ValidationIssue[];
}

function SkillTile(): ReactElement {
  return (
    <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
      <BookOpen className="size-4" aria-hidden="true" />
    </span>
  );
}

/**
 * The body of the Skills dock of a chat step or an agent step. It has the same
 * parts as the Tools dock: a search box, a section for the skills of the step
 * and one for the skills of the skills folder that it can add, one row for each
 * skill with its description and its state, and a detail area. A search that
 * names no listed skill offers the typed name, as the list can be missing.
 * The dock stays open after an add. Every edit is an `editStepSkills`
 * operation.
 */
export function SkillsDock({
  step,
  stepLabel,
  fieldIssues,
  locked,
  adding,
  onEdit,
  onClose,
}: SkillsDockProps): ReactElement {
  const { ts } = useI18n();
  const baseId = useId();
  const listId = `${baseId}-list`;
  const searchRef = useRef<HTMLInputElement>(null);
  const choices = useSkillChoices();
  const [query, setQuery] = useState('');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const words = useMemo(() => queryWords(query), [query]);
  const typed = query.trim();
  const entries = useMemo(() => readStepSkills(step.value), [step.value]);

  const byName = useMemo(
    () => new Map(choices.skills.map((skill) => [skill.name, skill])),
    [choices.skills]
  );
  const rows = useMemo(() => {
    const listed = new Set(entries.map((entry) => entry.name));
    const own: SkillRow[] = entries.map((entry) => {
      const choice = byName.get(entry.name);
      return {
        id: `step:${entry.index}`,
        group: 'step' as const,
        name: entry.name === '' ? ts('Invalid skill entry') : entry.name,
        description: choice?.description ?? '',
        index: entry.index,
        choice,
        problem: entry.problem ? ts(entry.problem) : undefined,
        issues: issuesUnder(fieldIssues, `with.skills[${entry.index}]`),
      };
    });
    const free: SkillRow[] = choices.skills
      .filter((skill) => !listed.has(skill.name))
      .map((skill) => ({
        id: `catalog:${skill.name}`,
        group: 'catalog' as const,
        name: skill.name,
        description: skill.description,
        choice: skill,
        issues: [],
      }));
    const keep = (list: SkillRow[]) =>
      list.filter((row) =>
        matchesWords(words, [
          row.name,
          row.description,
          row.choice?.reason ?? '',
        ])
      );
    const catalog = keep(free);
    // A name that no list shows can still be a skill: the list can fail to
    // load, or lack it. The typed name is an item only when nothing else fits.
    const offer =
      typed !== '' &&
      !/\s/.test(typed) &&
      !listed.has(typed) &&
      !free.some((row) => row.name === typed) &&
      (catalog.length === 0 || !choices.available)
        ? [
            {
              id: `typed:${typed}`,
              group: 'typed' as const,
              name: typed,
              description: ts('A skill that the list does not show.'),
              issues: [],
            },
          ]
        : [];
    return {
      step: keep(own),
      catalog: [...catalog, ...offer],
      total: own.length,
    };
  }, [
    entries,
    choices.skills,
    choices.available,
    byName,
    fieldIssues,
    words,
    typed,
    ts,
  ]);

  const ordered = useMemo(
    () =>
      adding
        ? [...rows.catalog, ...rows.step]
        : [...rows.step, ...rows.catalog],
    [adding, rows]
  );
  const active = ordered.find((row) => row.id === activeId) ?? ordered[0];
  const selected = ordered.find((row) => row.id === selectedId) ?? active;
  const rowDomId = (id: string) =>
    `${baseId}-${ordered.findIndex((row) => row.id === id)}`;

  const activate = (row: SkillRow | undefined) => {
    if (!row) return;
    setActiveId(row.id);
    setSelectedId(row.id);
  };
  useEffect(() => {
    if (active) {
      document.getElementById(rowDomId(active.id))?.scrollIntoView?.({
        block: 'nearest',
      });
    }
  }, [active?.id, ordered.length]);
  useEffect(() => {
    if (pending === null) return;
    const row = ordered.find((item) => item.id === pending);
    if (!row) return;
    activate(row);
    setPending(null);
  }, [pending, ordered]);

  const usable = (row: SkillRow) => row.choice?.usable !== false;
  const canAdd = (row: SkillRow) =>
    !locked && row.group !== 'step' && usable(row);
  const add = (row: SkillRow) => {
    if (!canAdd(row)) return;
    onEdit({
      kind: 'editStepSkills',
      step: step.key,
      change: { kind: 'add', name: row.name },
    });
    setPending(`step:${entries.length}`);
    if (row.group === 'typed') setQuery('');
  };
  const remove = (row: SkillRow) => {
    if (locked || row.index === undefined) return;
    onEdit({
      kind: 'editStepSkills',
      step: step.key,
      change: { kind: 'remove', index: row.index },
    });
  };

  const move = (to: number) => {
    if (ordered.length === 0) return;
    activate(ordered[Math.max(0, Math.min(to, ordered.length - 1))]);
  };
  const at = active ? ordered.findIndex((row) => row.id === active.id) : -1;
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return;
    switch (event.key) {
      case 'ArrowDown':
        move(at + 1);
        break;
      case 'ArrowUp':
        move(at - 1);
        break;
      case 'Home':
        if (event.shiftKey) return;
        move(0);
        break;
      case 'End':
        if (event.shiftKey) return;
        move(ordered.length - 1);
        break;
      case 'Enter':
        if (active && active.group !== 'step') add(active);
        break;
      case 'Delete':
        if (query !== '' || !active || active.group !== 'step') return;
        remove(active);
        break;
      case 'Escape':
        if (query !== '') setQuery('');
        else onClose();
        break;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
  };

  const stop = (event: { stopPropagation(): void }) => event.stopPropagation();
  const renderRow = (row: SkillRow): ReactElement => {
    const isActive = active?.id === row.id;
    const isSelected = selected?.id === row.id;
    const refused = row.choice?.usable === false;
    const missing =
      row.group === 'step' &&
      choices.available &&
      row.choice === undefined &&
      row.problem === undefined;
    return (
      <div
        key={row.id}
        id={rowDomId(row.id)}
        role="option"
        aria-selected={isSelected}
        data-testid="ve-skill-row"
        data-row={row.id}
        onClick={() => activate(row)}
        className={cn(
          'group mx-2 flex cursor-default items-start gap-3 rounded-md px-2 py-2',
          isSelected ? 'bg-accent text-accent-foreground' : 'hover:bg-muted/60',
          isActive && 'ring-1 ring-inset ring-ring/40',
          refused && row.group !== 'step' && 'opacity-70'
        )}
      >
        <SkillTile />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">
            {row.group === 'typed'
              ? ts('Add "{name}"', { name: row.name })
              : row.name}
          </span>
          <span
            className="block truncate text-xs text-muted-foreground"
            title={row.description}
          >
            {row.description || ts('No description')}
          </span>
          {(refused || missing || row.problem) && (
            <span className="mt-1 flex flex-wrap gap-1">
              {refused && (
                <span className="max-w-full truncate rounded bg-destructive/10 px-1.5 text-[11px] leading-4 text-destructive">
                  {ts('Error')}
                  {row.choice?.reason ? `: ${row.choice.reason}` : ''}
                </span>
              )}
              {missing && (
                <Chip
                  chip={{
                    text: ts('Not in the skills folder'),
                    tone: 'warning',
                  }}
                />
              )}
              {row.problem && (
                <span className="rounded bg-destructive/10 px-1.5 text-[11px] leading-4 text-destructive">
                  {row.problem}
                </span>
              )}
            </span>
          )}
        </span>
        <span className="flex shrink-0 flex-col items-end gap-1">
          <IssueBadge issues={row.issues} />
          <span
            aria-hidden="true"
            className={cn(
              'flex h-6 items-center',
              !isSelected && 'opacity-0 group-hover:opacity-100'
            )}
          >
            {row.group === 'step' && !locked && (
              <span
                role="presentation"
                title={ts('Remove skill {name}', { name: row.name })}
                onClick={(event) => {
                  stop(event);
                  remove(row);
                }}
                className="flex size-6 cursor-pointer items-center justify-center rounded hover:bg-background hover:text-destructive"
              >
                <Trash2 className="size-3.5" aria-hidden="true" />
              </span>
            )}
            {row.group !== 'step' && canAdd(row) && (
              <span
                role="presentation"
                title={ts('Add')}
                onClick={(event) => {
                  stop(event);
                  add(row);
                }}
                className="flex h-6 cursor-pointer items-center gap-0.5 rounded px-1.5 text-[11px] font-medium hover:bg-background"
              >
                <Plus className="size-3" aria-hidden="true" />
                {ts('Add')}
              </span>
            )}
          </span>
        </span>
      </div>
    );
  };

  const detail = selected ? (
    <SkillDetail
      key={selected.id}
      row={selected}
      stepLabel={stepLabel}
      agent={isAgentRunValue(step.value, step.action)}
      locked={locked}
      canAdd={canAdd(selected)}
      onAdd={() => add(selected)}
      onRemove={() => remove(selected)}
    />
  ) : (
    <div data-testid="ve-dock-detail-empty" className="space-y-2 p-4">
      <h2 className="text-sm font-semibold">{ts('Choose a skill')}</h2>
      <p className="text-xs text-muted-foreground">
        {ts('Select a skill in the list to see its details here.')}
      </p>
    </div>
  );

  const noRows = ordered.length === 0;
  return (
    <div
      data-testid="ve-skills-dock"
      data-mode={adding ? 'add' : 'list'}
      onKeyDown={(event) => {
        if (event.key === '/' && !(event.target instanceof HTMLInputElement)) {
          event.preventDefault();
          searchRef.current?.focus();
        }
      }}
      className="@container flex min-h-0 flex-1 flex-col"
    >
      <div className="border-b border-border p-3">
        <DockSearch
          value={query}
          label={ts('Search skills')}
          placeholder={ts('Search skills, or type a skill name')}
          listId={listId}
          activeId={active ? rowDomId(active.id) : undefined}
          inputRef={searchRef}
          keyShortcuts="/"
          onChange={(value) => {
            setQuery(value);
            setActiveId(null);
            setSelectedId(null);
          }}
          onKeyDown={onKeyDown}
        />
      </div>
      <div className="flex min-h-0 flex-1 flex-col @2xl:flex-row">
        <div className="flex min-h-0 flex-1 flex-col @2xl:w-1/2 @2xl:flex-none @2xl:border-r @2xl:border-border">
          <div
            id={listId}
            role="listbox"
            aria-label={ts('Skills')}
            onMouseDown={(event) => event.preventDefault()}
            className="min-h-0 flex-1 overflow-y-auto pb-2"
          >
            {noRows && query !== '' && (
              <p className="px-4 py-6 text-center text-sm text-muted-foreground">
                {ts('No skill matches the search.')}
              </p>
            )}
            {(adding ? ['catalog', 'step'] : ['step', 'catalog']).map(
              (group) => {
                const list = group === 'step' ? rows.step : rows.catalog;
                if (group === 'step' && list.length === 0 && query === '') {
                  return (
                    <Fragment key={group}>
                      <GroupLabel title={ts('In this step')} count={0} />
                      <p className="px-4 py-2 text-xs text-muted-foreground">
                        {locked
                          ? ts('No skills')
                          : ts('No skills yet. Pick a skill below.')}
                      </p>
                    </Fragment>
                  );
                }
                if (list.length === 0 && group === 'catalog' && query === '') {
                  return (
                    <Fragment key={group}>
                      <GroupLabel title={ts('Available to add')} count={0} />
                      <p
                        data-testid={
                          choices.available
                            ? 've-skill-empty'
                            : 've-add-skill-note'
                        }
                        className="px-4 py-2 text-xs text-muted-foreground"
                      >
                        {choices.available
                          ? ts(
                              'Every skill of the skills folder is in this step already.'
                            )
                          : ts(
                              'The list of skills could not be loaded. Type a name instead.'
                            )}
                      </p>
                    </Fragment>
                  );
                }
                if (list.length === 0) return null;
                return (
                  <Fragment key={group}>
                    <GroupLabel
                      title={
                        group === 'step'
                          ? ts('In this step')
                          : ts('Available to add')
                      }
                      count={list.length}
                    />
                    {list.map(renderRow)}
                  </Fragment>
                );
              }
            )}
            {!choices.available && query !== '' && (
              <p
                data-testid="ve-add-skill-note"
                className="px-4 py-2 text-xs text-muted-foreground"
              >
                {ts(
                  'The list of skills could not be loaded. Type a name instead.'
                )}
              </p>
            )}
          </div>
        </div>
        <section
          aria-label={ts('Details of the skill')}
          data-testid="ve-dock-detail"
          className="max-h-[50%] min-h-0 shrink-0 overflow-y-auto border-t border-border @2xl:max-h-none @2xl:min-w-0 @2xl:flex-1 @2xl:border-t-0"
        >
          {detail}
        </section>
      </div>
      <footer className="border-t border-border px-4 py-2 text-[11px] text-muted-foreground">
        <p className="truncate">
          {ts(
            'Up and Down to move, Enter to add, Delete to remove, / to search'
          )}
        </p>
      </footer>
    </div>
  );
}

function SkillDetail({
  row,
  stepLabel,
  agent,
  locked,
  canAdd,
  onAdd,
  onRemove,
}: {
  row: SkillRow;
  stepLabel: string;
  agent: boolean;
  locked: boolean;
  canAdd: boolean;
  onAdd(): void;
  onRemove(): void;
}): ReactElement {
  const { ts } = useI18n();
  const page = row.group === 'typed' ? null : skillLink(row.name);
  return (
    <div data-testid="ve-dock-detail-body" className="space-y-3 p-4">
      <header className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
          <BookOpen className="size-5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="break-words text-sm font-semibold leading-5">
            {row.name}
          </h2>
          <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
            <span className="rounded bg-muted px-1 text-[10px] font-medium leading-4 text-muted-foreground">
              {ts('Skill')}
            </span>
            <IssueBadge issues={row.issues} />
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {row.group === 'step' ? (
            !locked && (
              <IconButton
                tone="destructive"
                icon={Trash2}
                label={ts('Remove skill {name}', { name: row.name })}
                onClick={onRemove}
              />
            )
          ) : (
            <Button
              type="button"
              size="sm"
              disabled={!canAdd}
              className="h-7 gap-1 px-2 text-xs"
              onClick={onAdd}
            >
              <Plus className="size-3.5" aria-hidden="true" />
              {ts('Add to {name}', { name: stepLabel })}
            </Button>
          )}
        </div>
      </header>
      {row.issues.length > 0 && (
        <section>
          <DetailHeading>{ts('Issues')}</DetailHeading>
          <FieldIssues issues={row.issues} className="mt-0" />
        </section>
      )}
      {row.choice?.usable === false && (
        <p role="alert" className="text-xs text-destructive">
          {ts('The server refuses this skill: {reason}', {
            reason: row.choice.reason || ts('It has an error.'),
          })}
        </p>
      )}
      {row.problem && <p className="text-xs text-destructive">{row.problem}</p>}
      <section>
        <DetailHeading>{ts('Description')}</DetailHeading>
        {row.description ? (
          <p className="whitespace-pre-line break-words text-xs">
            {row.description}
          </p>
        ) : (
          <p className="text-xs italic text-muted-foreground">
            {ts('This skill has no description.')}
          </p>
        )}
        {page && (
          <div className="mt-1.5">
            <OpenTextLink page={page}>
              <I18nText text="Open the skill" />
            </OpenTextLink>
          </div>
        )}
      </section>
      <p className="text-[11px] text-muted-foreground">
        {agent
          ? ts('The CLI reads a skill with its tool Skill.')
          : ts('The model reads a skill with the tool load_skill.')}
      </p>
    </div>
  );
}
