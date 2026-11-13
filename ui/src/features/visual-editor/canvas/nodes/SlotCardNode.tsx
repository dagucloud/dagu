// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BookOpen, Plus, Wrench, type LucideIcon } from 'lucide-react';
import {
  memo,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode,
} from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ToolSelection } from '../../contracts';
import { builtinIcon, builtinLabel } from '../../tools/builtin';
import { KindBadge, KindIcon } from '../../tools/KindBadge';
import {
  builtinTooltip,
  entryIcon,
  entryTooltip,
  kindClasses,
  kindGroupTitle,
  kindOfCategory,
  type ToolKind,
} from '../../tools/kinds';
import {
  entryKey,
  type BuiltinEntryModel,
  type ToolEntryModel,
} from '../../tools/model';
import { OpenIcon } from '../../tools/OpenLink';
import { entryLink, skillLink, type PageLink } from '../../tools/targets';
import { useSkillChoices } from '../../tools/useSkillChoices';
import { useCanvasActions } from '../context';
import type { NodeProps } from '../engine';
import { SLOT_CARD_HEADER, SLOT_CARD_MAX_ROWS, SLOT_CARD_ROW } from '../subRow';
import { planToolCard, type ToolItem } from '../toolGroups';
import { entryMark } from '../toolSatellites';
import { useToolDefinitions, useToolRun, type ToolRunInfo } from '../toolRun';
import type { SlotCardData, StepFlowNode } from '../types';
import type { SkillEntry } from '../chatModel';
import type { SkillChoice } from '../../tools/useSkillChoices';
import { CARD_OUTLINE } from './NodeParts';
import { SUB_SELECTED_CLASS, SubHandle, SubTile } from './SubLeaf';
import {
  CallsBadge,
  ToolLimitsRow,
  FixedLock,
  callsOf,
  fixedLines,
  sameTool,
  totalCallsOf,
} from './ToolSatelliteParts';

type Translate = ReturnType<typeof useI18n>['ts'];

interface RowView {
  key: string;
  kind: ToolKind;
  /** The icon of the tool; the icon of its kind when absent. */
  icon?: LucideIcon;
  label: string;
  /** The muted cue at the right of the row. */
  cue?: string | undefined;
  /** The cue is a fault. */
  cueError?: boolean;
  /** Small marks before the cue: the lock, the calls. */
  marks?: ReactNode;
  title: string;
  /** The tool entry that a click selects. */
  tool: ToolSelection;
  attrs?: Record<string, string | boolean | undefined>;
  /** The page that the row opens in a new tab. */
  link?: PageLink | null;
}

function toolRow(
  ts: Translate,
  card: SlotCardData,
  entry: ToolEntryModel,
  run: ToolRunInfo | null,
  defs: ReturnType<typeof useToolDefinitions>
): RowView {
  const mark = entryMark(entry, defs);
  let label = entry.label;
  let cue: string | undefined;
  let cueError = false;
  let calls: number | undefined;
  switch (entry.kind) {
    case 'dag':
      calls = callsOf(run, card.stepName, [entry.label]);
      break;
    case 'mcp':
      // A new entry has no name yet.
      if (label === '') label = entry.mcp?.host || ts('MCP server');
      if (entry.mcp && entry.mcp.allow.length > 0) {
        cue = ts(
          entry.mcp.allow.length === 1 ? '{count} tool' : '{count} tools',
          {
            count: entry.mcp.allow.length,
          }
        );
      }
      break;
    case 'invalid':
      label = ts('Invalid tool entry');
      cue = entry.problem;
      cueError = true;
      break;
  }
  const title = [...entryTooltip(ts, entry), ...fixedLines(entry.params)]
    .filter(Boolean)
    .join('\n');
  return {
    key: entryKey(entry),
    kind: kindOfCategory(entry.category),
    icon: entryIcon(entry.kind),
    label,
    cue,
    cueError,
    marks: (
      <>
        <FixedLock params={entry.params} />
        <CallsBadge count={calls} />
      </>
    ),
    title,
    tool: { step: card.owner, category: entry.category, index: entry.index },
    link: entryLink(entry),
    attrs: {
      'data-kind': entry.kind,
      'data-offered': mark.state === 'unknown' ? undefined : true,
    },
  };
}

/** A row of the Tools card for a built-in tool of an agent step. */
function builtinRow(
  ts: Translate,
  card: SlotCardData,
  entry: BuiltinEntryModel
): RowView {
  const cue =
    entry.name === 'shell'
      ? ts('{count} allowed', { count: (entry.allow ?? []).length })
      : undefined;
  return {
    key: `builtin:${entry.name}`,
    kind: 'builtin',
    icon: builtinIcon(entry.name),
    label: builtinLabel(ts, entry.name),
    cue,
    title: builtinTooltip(ts, entry).join('\n'),
    // The built-in tools of a step are one entry: one panel edits them all.
    tool: { step: card.owner, category: 'builtin', index: 0 },
    attrs: { 'data-kind': 'builtin' },
  };
}

/** The view of a row of the Skills card. */
interface SkillRowView {
  key: string;
  label: string;
  title: string;
  /** The step fails with this skill: the server refuses it, or the directory has none of this name. */
  unusable?: boolean;
  /** The page that the row opens in a new tab. */
  link?: PageLink | null;
}

/**
 * A row of the Skills card: the name, the description as tooltip. A click
 * opens the dock of the Skills list.
 */
function skillRow(
  ts: ReturnType<typeof useI18n>['ts'],
  skill: SkillEntry,
  known: readonly SkillChoice[],
  listed: boolean
): SkillRowView {
  const choice = known.find((item) => item.name === skill.name);
  // A name that the skills directory does not have: the list has arrived and lacks it.
  const missing = listed && !choice;
  return {
    key: `${skill.index}`,
    label: skill.name,
    title: [
      skill.name,
      missing
        ? ts('Skill not found in the skills directory')
        : choice?.usable === false
          ? choice.reason
          : choice?.description,
    ]
      .filter(Boolean)
      .join('\n'),
    unusable: missing || choice?.usable === false,
    link: skillLink(skill.name),
  };
}

/** A row of the Skills card: the icon of a skill, its name and its open icon. */
function SkillLine({
  view,
  height,
  onOpen,
}: {
  view: SkillRowView;
  height: number;
  onOpen(): void;
}) {
  return (
    <div
      data-testid="ve-canvas-slot-line"
      style={{ height }}
      className="group/row flex w-full items-center gap-1 pl-1.5 pr-1.5 hover:bg-muted/60 focus-within:bg-muted/60"
    >
      <div
        data-kind="skill"
        data-testid="ve-canvas-slot-row"
        role="button"
        tabIndex={0}
        aria-label={view.label}
        title={view.title}
        onKeyDown={(event: KeyboardEvent) => {
          if (event.key !== 'Enter' && event.key !== ' ') return;
          event.preventDefault();
          event.stopPropagation();
          onOpen();
        }}
        onClick={(event: MouseEvent) => {
          event.stopPropagation();
          onOpen();
        }}
        // The dock of the list is open already: the card must not open it again.
        onDoubleClick={(event: MouseEvent) => event.stopPropagation()}
        className="flex h-full min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        <BookOpen
          className={cn(
            'h-3.5 w-3.5 shrink-0',
            view.unusable ? 'text-destructive' : 'text-muted-foreground'
          )}
          aria-hidden="true"
        />
        <span
          className={cn(
            'min-w-0 flex-1 truncate text-xs font-medium',
            view.unusable && 'text-destructive'
          )}
        >
          {view.label}
        </span>
      </div>
      <OpenIcon
        link={view.link ?? null}
        name={view.label}
        className="opacity-0 group-hover/row:opacity-100 group-focus-within/row:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
      />
    </div>
  );
}

function Row({
  view,
  height,
  selected,
  onOpen,
}: {
  view: RowView;
  height: number;
  selected: boolean;
  onOpen(tool: ToolSelection): void;
}) {
  const open = () => onOpen(view.tool);
  return (
    // The row is a box with its controls side by side: the body selects the
    // tool, the open icon is its own control.
    <div
      data-testid="ve-canvas-slot-line"
      style={{ height }}
      className={cn(
        'group/row flex w-full items-center gap-1 pl-1.5 pr-1.5 hover:bg-muted/60 focus-within:bg-muted/60',
        selected && 'bg-primary/10'
      )}
    >
      <div
        {...view.attrs}
        data-testid="ve-canvas-slot-row"
        data-selected={selected || undefined}
        role="button"
        tabIndex={0}
        aria-label={view.label}
        aria-current={selected || undefined}
        title={view.title}
        onKeyDown={(event: KeyboardEvent) => {
          if (event.key !== 'Enter' && event.key !== ' ') return;
          event.preventDefault();
          event.stopPropagation();
          open();
        }}
        onClick={(event: MouseEvent) => {
          // The tool is the selection: the node must not select its step again.
          event.stopPropagation();
          open();
        }}
        // The panel of the tool is open already: the card must not open the form.
        onDoubleClick={(event: MouseEvent) => event.stopPropagation()}
        className="flex h-full min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        <KindIcon kind={view.kind} icon={view.icon} className="h-3.5 w-3.5" />
        <span className="min-w-0 flex-1 truncate text-xs font-medium">
          {view.label}
        </span>
        {view.marks}
        {view.cue && (
          <span
            className={cn(
              'max-w-[45%] shrink-0 truncate text-right text-[10px]',
              view.cueError ? 'text-destructive' : 'text-muted-foreground'
            )}
          >
            {view.cue}
          </span>
        )}
        <KindBadge kind={view.kind} />
      </div>
      <OpenIcon
        link={view.link ?? null}
        name={view.label}
        className="opacity-0 group-hover/row:opacity-100 group-focus-within/row:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
      />
    </div>
  );
}

/** The heading of a group of tools of one kind, in the colour of the kind. */
function Heading({
  kind,
  count,
  height,
}: {
  kind: ToolKind;
  count: number;
  height: number;
}) {
  const { ts } = useI18n();
  return (
    <div
      data-testid="ve-canvas-slot-heading"
      data-kind={kind}
      style={{ height }}
      className={cn(
        'flex w-full items-center gap-1.5 px-2.5 text-[10px] font-semibold uppercase tracking-wide',
        kindClasses(kind).text
      )}
    >
      <span className="min-w-0 truncate">{kindGroupTitle(ts, kind)}</span>
      <span className="font-normal text-muted-foreground">{count}</span>
    </div>
  );
}

/**
 * The card of the Tools or the Skills slot of a chat step or an agent step, as
 * a small form block. The Skills card has a header with the count, one row for
 * each skill (the icon, the name, an open icon that opens its page), and a last
 * row that adds one; a click on a row opens the dock of the list, as a click on
 * a tool row opens the panel of the tool.
 * The Tools card is a form block: a header with the slot name and the number of tools, the tools in
 * groups by kind (MCP server, Built-in tool), at most
 * eight of them, then a "+N more" row, and a last row that adds one. A group
 * has a small heading when the step has more than one kind of tool. With
 * more than one kind, above eight tools or when one column would pass ten
 * rows, each kind lists up to eight tools with its own "+N more" row, and the
 * kinds share two or more columns of equal width; the header, the limits and the add row span all the columns. A row has
 * the icon and the badge of its kind, the name, and at its right a muted cue:
 * the tools of an MCP server, the calls of the shown run. Its tooltip names
 * the kind and the source. A row that the run did not offer is grey. A click,
 * Enter or Space on a row selects the tool and opens its panel at the right;
 * the add row opens the dock that picks a tool to add. A double click on the
 * header or on the body outside a row, or Enter or Space on the focused card,
 * opens the dock at the right with the whole list (`openSlotPanel`). A row
 * that leads to a page has an open icon, which opens it in a new tab. A step
 * puts its DAG tools on the DAGs card (`DagCardNode`), so this card gets none
 * from the canvas; it still draws a DAG row that it is given. A compact canvas
 * draws a tile (`SubTile`).
 */
function SlotCardNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { node } = data;
  const card = node.slotCard;
  const { ts } = useI18n();
  const { compact = false, onCommand, selectedTool } = useCanvasActions();
  const run = useToolRun();
  const defs = useToolDefinitions(card?.stepName, !!card?.active);
  const skillList = useSkillChoices(card?.slot === 'skills');
  if (!card) return null;
  const tools = card.slot === 'tools';
  const header = SLOT_CARD_HEADER;
  const height = SLOT_CARD_ROW;
  const viewOf = (item: ToolItem): RowView =>
    item.type === 'entry'
      ? toolRow(ts, card, item.entry, run, defs)
      : builtinRow(ts, card, item.entry);
  const plan = planToolCard(card.entries, card.builtin ?? []);
  const count = tools
    ? card.entries.length + (card.builtin ?? []).length
    : card.skills.length;
  const open = (tool: ToolSelection) => onCommand({ kind: 'openTool', tool });
  const title = tools ? ts('Tools') : ts('Skills');
  const addLabel = tools ? ts('Add a tool') : ts('Add a skill');
  const nothing = tools ? ts('No tools') : ts('No skills');
  const rows = (items: ToolItem[]) =>
    items.map((item) => {
      const view = viewOf(item);
      return (
        <Row
          key={view.key}
          view={view}
          height={height}
          selected={sameTool(selectedTool, view.tool)}
          onOpen={open}
        />
      );
    });
  const skillViews = card.skills.map((skill) =>
    skillRow(ts, skill, skillList.skills, skillList.available)
  );
  const columnar = plan.columns.length > 1;
  // The dock at the right, with the whole list: the edit of the slot.
  const openPanel = () =>
    onCommand({
      kind: 'openSlotPanel',
      step: card.owner,
      slot: card.slot,
    });
  if (compact) {
    const names = tools
      ? [
          ...card.entries.map(
            (entry) => viewOf({ type: 'entry', entry }).label
          ),
          ...(card.builtin ?? []).map((entry) => builtinLabel(ts, entry.name)),
        ]
      : skillViews.map((view) => view.label);
    const used = tools ? totalCallsOf(run, card.stepName) : undefined;
    const callsLine = used ? [ts('Calls: {count}', { count: used })] : [];
    // A tool of the step that the panel shows is a tool of this tile.
    const chosen =
      tools &&
      selectedTool?.step === card.owner &&
      selectedTool.category !== 'dags';
    const problem = tools
      ? card.entries.some((entry) => entry.kind === 'invalid')
        ? ts('Invalid tool entry')
        : undefined
      : skillViews.find((view) => view.unusable)?.title;
    return (
      <SubTile
        slot={card.slot}
        selected={selected || chosen}
        icon={tools ? Wrench : BookOpen}
        label={title}
        count={count}
        title={[title, ...names, ...callsLine].join('\n')}
        problem={problem}
        attrs={{
          'data-testid': `ve-canvas-node-${node.key}`,
          'data-shape': 'slotcard',
          'data-slot': card.slot,
        }}
        onOpen={openPanel}
      />
    );
  }
  // The "+N more" row opens the first hidden tool, or the dock of the list.
  const moreRow = (hiddenCount: number, first?: ToolItem) => {
    if (hiddenCount === 0) return null;
    const openHidden = () => {
      if (first) open(viewOf(first).tool);
      else openPanel();
    };
    return (
      <div
        role="button"
        tabIndex={0}
        data-testid="ve-canvas-slot-more"
        aria-label={ts('+{count} more', { count: hiddenCount })}
        style={{ height }}
        onClick={(event) => {
          event.stopPropagation();
          openHidden();
        }}
        onDoubleClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return;
          event.preventDefault();
          event.stopPropagation();
          openHidden();
        }}
        className="flex w-full cursor-pointer items-center px-2.5 text-[11px] text-muted-foreground outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        {ts('+{count} more', { count: hiddenCount })}
      </div>
    );
  };
  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="slotcard"
      data-slot={card.slot}
      role="group"
      aria-label={title}
      aria-current={selected || undefined}
      data-selected={selected || undefined}
      // The card takes the focus: Enter or Space on it, as a double click on
      // the header or on the body outside a row, opens the dock of the list at
      // the right. A row and the add row have their own keys; a tool row and
      // the add row stop their double click.
      tabIndex={0}
      onKeyDown={(event) => {
        if (event.target !== event.currentTarget) return;
        if (event.key !== 'Enter' && event.key !== ' ') return;
        event.preventDefault();
        event.stopPropagation();
        openPanel();
      }}
      onDoubleClick={(event) => {
        event.stopPropagation();
        openPanel();
      }}
      className={cn(
        'group relative flex h-full w-full flex-col rounded-md bg-card text-card-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring',
        CARD_OUTLINE,
        selected && SUB_SELECTED_CLASS
      )}
    >
      <SubHandle slot={card.slot} />
      <div
        style={{ height: header }}
        className="flex shrink-0 items-center gap-1.5 border-b border-border/60 px-2.5"
      >
        <span className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {title}
        </span>
        <span
          data-testid="ve-canvas-slot-count"
          className="ml-auto rounded-full bg-muted px-1.5 text-[10px] leading-4 text-muted-foreground"
        >
          {count}
        </span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col py-px">
        {tools && card.limits && (
          <ToolLimitsRow
            limits={card.limits}
            used={totalCallsOf(run, card.stepName)}
            height={height}
          />
        )}
        {!tools &&
          skillViews
            .slice(0, SLOT_CARD_MAX_ROWS)
            .map((view) => (
              <SkillLine
                key={view.key}
                view={view}
                height={height}
                onOpen={openPanel}
              />
            ))}
        {tools && (
          // One column, or the columns of the plan (see `planToolCard`).
          <div className="flex">
            {plan.columns.map((groups, index) => (
              <div
                key={groups[0]?.kind}
                data-testid="ve-tools-column"
                className={cn(
                  'min-w-0 flex-1',
                  index > 0 && 'border-l border-border/60'
                )}
              >
                {groups.map((group) =>
                  plan.headed ? (
                    // A reader of the screen hears the kind once, at the group.
                    <div
                      key={group.kind}
                      role="group"
                      aria-label={kindGroupTitle(ts, group.kind)}
                    >
                      <Heading
                        kind={group.kind}
                        count={group.total}
                        height={height}
                      />
                      {rows(group.items)}
                      {moreRow(group.hidden.length, group.hidden[0])}
                    </div>
                  ) : (
                    rows(group.items)
                  )
                )}
                {!columnar && moreRow(plan.hidden.length, plan.hidden[0])}
              </div>
            ))}
          </div>
        )}
        {!tools && moreRow(Math.max(0, skillViews.length - SLOT_CARD_MAX_ROWS))}
        {card.editable ? (
          <button
            type="button"
            data-testid="ve-canvas-sub-add"
            aria-label={addLabel}
            title={addLabel}
            style={{ height }}
            onClick={(event) => {
              event.stopPropagation();
              onCommand({
                kind: 'openSlotDock',
                step: card.owner,
                slot: card.slot,
              });
            }}
            onDoubleClick={(event) => event.stopPropagation()}
            className="nodrag nopan flex w-full items-center gap-1.5 px-2.5 text-left text-xs text-muted-foreground outline-none hover:bg-primary/10 hover:text-primary focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          >
            <Plus className="h-3.5 w-3.5" aria-hidden="true" />
            {addLabel}
          </button>
        ) : (
          count === 0 && (
            <span
              style={{ height }}
              className="flex items-center px-2.5 text-xs text-muted-foreground"
            >
              {nothing}
            </span>
          )
        )}
      </div>
    </div>
  );
}

export const SlotCardNode = memo(SlotCardNodeComponent);
