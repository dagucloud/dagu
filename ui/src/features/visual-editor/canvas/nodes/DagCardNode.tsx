// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Maximize2, Plus, Repeat, Workflow } from 'lucide-react';
import {
  memo,
  useMemo,
  type KeyboardEvent,
  type MouseEvent,
  type ReactElement,
} from 'react';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ToolSelection } from '../../contracts';
import { KindIcon } from '../../tools/KindBadge';
import { entryIcon, entryTooltip } from '../../tools/kinds';
import { mergeInputs, type InputRow } from '../../tools/dockModel';
import { InputChip } from '../../tools/InputChip';
import { toolNameOf, type ToolEntryModel } from '../../tools/model';
import { OpenIcon } from '../../tools/OpenLink';
import { dagLink } from '../../tools/targets';
import { useDagDetails } from '../../tools/useDagCatalog';
import { useCanvasActions } from '../context';
import type { NodeProps } from '../engine';
import { DagMiniature, MINI_BOX } from '../DagMiniature';
import { buildMiniGraph, type MiniGraph } from '../miniGraph';
import {
  DAG_CARD_MAX_ENTRIES,
  DAG_ENTRY,
  SLOT_CARD_HEADER,
  SLOT_CARD_ROW,
} from '../subRow';
import { entryMark } from '../toolSatellites';
import { useDagToolRun, useToolDefinitions, useToolRun } from '../toolRun';
import type { SlotCardData, StepFlowNode } from '../types';
import { useOnScreen } from '../useOnScreen';
import { CARD_OUTLINE } from './NodeParts';
import { SUB_SELECTED_CLASS, SubHandle, SubTile } from './SubLeaf';
import {
  CallsBadge,
  FixedLock,
  ToolLimitsRow,
  callsOf,
  fixedLines,
  sameTool,
  totalCallsOf,
} from './ToolSatelliteParts';

/** The inputs that the entry shows as chips: the others are counted. */
const CHIPS_SHOWN = 3;

type Translate = ReturnType<typeof useI18n>['ts'];

/** The one line of inputs of an entry: up to three chips, then how many more. */
function InputsLine({
  inputs,
  quiet,
  height,
  ts,
}: {
  inputs: InputRow[];
  /** The DAG is not loaded or not known: "No input" would be a claim that nobody checked. */
  quiet: boolean;
  height: number;
  ts: Translate;
}): ReactElement {
  const shown = inputs.slice(0, CHIPS_SHOWN);
  const more = inputs.length - shown.length;
  return (
    <div
      data-testid="ve-dag-inputs"
      style={{ height }}
      className="flex items-center gap-1 overflow-hidden px-2.5"
    >
      {inputs.length === 0 ? (
        <span className="text-[10px] text-muted-foreground">
          {quiet ? '' : ts('No input')}
        </span>
      ) : (
        <>
          {shown.map((input) => (
            <InputChip key={input.name} input={input} />
          ))}
          {more > 0 && (
            <span
              title={inputs
                .slice(CHIPS_SHOWN)
                .map((input) => input.name)
                .join('\n')}
              className="shrink-0 text-[10px] text-muted-foreground"
            >
              {ts('+{count} more', { count: more })}
            </span>
          )}
        </>
      )}
    </div>
  );
}

/** What the box of the miniature of an entry shows. */
type MiniState =
  | 'graph'
  | 'summary'
  | 'loading'
  | 'empty'
  | 'missing'
  | 'dynamic'
  | 'invalid';

/** The dashed box of an entry that has no graph to show, with the reason in words. */
function MiniNote({
  state,
  problem,
  ts,
}: {
  state: Exclude<MiniState, 'graph' | 'summary'>;
  problem: string | undefined;
  ts: Translate;
}): ReactElement {
  const text = {
    loading: '',
    empty: ts('No steps'),
    missing: ts('DAG not found'),
    dynamic: ts('Chosen when the step runs'),
    invalid: problem ?? ts('Invalid tool entry'),
  }[state];
  const fault = state === 'missing' || state === 'invalid';
  return (
    <div
      data-testid="ve-dag-mini-note"
      data-state={state}
      role={fault ? 'alert' : undefined}
      className={cn(
        'flex h-full w-full items-center justify-center rounded-md border border-dashed px-2 text-center text-[11px]',
        fault
          ? 'border-destructive/60 text-destructive'
          : 'border-border text-muted-foreground',
        state === 'loading' && 'motion-safe:animate-pulse bg-muted/40'
      )}
    >
      <span className="truncate">{text}</span>
    </div>
  );
}

function DagEntry({
  card,
  entry,
  selected,
  separated,
  onOpen,
  onEnlarge,
}: {
  card: SlotCardData;
  entry: ToolEntryModel;
  selected: boolean;
  /** A line over the entry: it follows the limits or another entry. */
  separated: boolean;
  onOpen(tool: ToolSelection): void;
  onEnlarge(tool: ToolSelection): void;
}): ReactElement {
  const { ts } = useI18n();
  const { dag: host } = useCanvasActions();
  const parts = DAG_ENTRY;
  const run = useToolRun();
  const defs = useToolDefinitions(card.stepName, !!card.active);
  const [watch, visible] = useOnScreen<HTMLDivElement>();
  const name = entry.label;
  const valid = entry.kind === 'dag';
  const details = useDagDetails(name, visible && valid);
  const child = useDagToolRun(card.stepName, name, visible, !!card.active);
  const mark = entryMark(entry, defs);
  const calls = valid ? callsOf(run, card.stepName, [name]) : undefined;
  const tool: ToolSelection = {
    step: card.owner,
    category: entry.category,
    index: entry.index,
  };
  const missing = valid && details.known === false;
  const recursive =
    valid &&
    host !== undefined &&
    name.trim() !== '' &&
    name.trim() === host.name;
  const mini = useMemo<MiniGraph | null>(
    () => (details.dag ? buildMiniGraph(details.dag, MINI_BOX) : null),
    [details.dag]
  );
  const inputs = useMemo(
    () =>
      mergeInputs(entry.params, details.known === true ? details.inputs : null),
    [entry.params, details.known, details.inputs]
  );
  const state: MiniState = !valid
    ? 'invalid'
    : missing
      ? 'missing'
      : details.dynamic
        ? 'dynamic'
        : !mini
          ? 'loading'
          : mini.kind === 'empty'
            ? 'empty'
            : mini.kind;
  const steps = mini && mini.kind !== 'empty' ? mini.stats.steps : undefined;
  const toolName = toolNameOf(name);
  const label = !valid
    ? ts('Invalid tool entry')
    : name === ''
      ? ts('DAG tool')
      : name;
  const title = [
    ...entryTooltip(ts, entry),
    ...(valid && toolName !== name && name !== ''
      ? [ts('The model calls it {name}', { name: toolName })]
      : []),
    ...(missing
      ? [
          ts(
            'The server does not know the DAG "{name}". The step fails when it starts.',
            { name }
          ),
        ]
      : []),
    ...fixedLines(entry.params),
  ]
    .filter(Boolean)
    .join('\n');
  const stepsText =
    steps === undefined
      ? ''
      : ts(steps === 1 ? '{count} step' : '{count} steps', { count: steps });
  // A summary has no step to read: the text tells how deep the flow goes.
  const summaryText =
    mini?.kind === 'summary'
      ? ts('{steps} steps in {count} stages', {
          steps: mini.stats.steps,
          count: mini.stats.depth,
        })
      : stepsText;
  const onKey = (event: KeyboardEvent, action: () => void) => {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    event.stopPropagation();
    action();
  };
  const stop = (event: MouseEvent) => event.stopPropagation();
  return (
    <div
      ref={watch}
      data-testid="ve-dag-entry"
      data-dag={name}
      data-state={state}
      data-offered={mark.state === 'unknown' ? undefined : true}
      data-selected={selected || undefined}
      className="group/row"
    >
      <div
        style={{ height: parts.title }}
        // The line is inside the height of the row: the layout counts it.
        className={cn(
          'flex w-full items-center gap-1 pl-1.5 pr-1.5 hover:bg-muted/60 focus-within:bg-muted/60',
          separated && 'border-t border-border/60',
          selected && 'bg-primary/10'
        )}
      >
        <div
          data-testid="ve-canvas-slot-row"
          data-kind="dag"
          data-selected={selected || undefined}
          role="button"
          tabIndex={0}
          aria-label={label}
          aria-current={selected || undefined}
          title={title}
          onKeyDown={(event) => onKey(event, () => onOpen(tool))}
          onClick={(event) => {
            stop(event);
            onOpen(tool);
          }}
          // The panel of the tool is open already: the card must not open the list.
          onDoubleClick={stop}
          className="flex h-full min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        >
          {missing || !valid ? (
            // The red icon says the DAG cannot load, as the Skills card does.
            <KindIcon
              kind="dag"
              icon={entryIcon('invalid')}
              className="h-3.5 w-3.5 text-destructive"
            />
          ) : (
            <KindIcon
              kind="dag"
              icon={entryIcon('dag')}
              className="h-3.5 w-3.5"
            />
          )}
          <span
            className={cn(
              'min-w-0 truncate text-xs font-medium',
              missing && 'text-destructive'
            )}
          >
            {label}
          </span>
          {valid && toolName !== name && name !== '' && (
            <span className="min-w-0 max-w-[40%] shrink truncate font-mono text-[10px] text-muted-foreground">
              {toolName}
            </span>
          )}
          {recursive && (
            <span
              role="img"
              data-testid="ve-dag-recursive"
              aria-label={ts('This DAG calls itself.')}
              title={ts('This DAG calls itself.')}
              className="inline-flex shrink-0 text-muted-foreground"
            >
              <Repeat className="h-3 w-3" aria-hidden="true" />
            </span>
          )}
          <FixedLock params={entry.params} />
          <CallsBadge count={calls} />
        </div>
        {child && (
          <span className="flex shrink-0 items-center gap-1">
            {child.status !== undefined && (
              <StatusDot kind="run" status={child.status} />
            )}
            {child.link && (
              <OpenIcon
                link={child.link}
                name={ts('{name} run', { name: label })}
                className="opacity-100"
              />
            )}
          </span>
        )}
        <OpenIcon
          link={valid ? dagLink(name) : null}
          name={label}
          className="opacity-0 group-hover/row:opacity-100 group-focus-within/row:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
        />
      </div>
      <p
        data-testid="ve-dag-description"
        style={{ height: parts.description }}
        title={details.description || undefined}
        className={cn(
          'truncate px-2.5 text-[11px] leading-4',
          details.description
            ? 'text-muted-foreground'
            : 'italic text-muted-foreground/80'
        )}
      >
        {details.description ||
          (details.known === true ? ts('No description') : '')}
      </p>
      <div
        style={{ height: parts.graph }}
        className="px-2 py-[3px]"
        data-testid="ve-dag-mini"
      >
        {mini && mini.kind !== 'empty' && state !== 'missing' ? (
          <button
            type="button"
            data-testid="ve-dag-mini-open"
            aria-label={ts('Show the graph of {name}', { name: label })}
            title={ts('Show the graph of {name}', { name: label })}
            onClick={(event) => {
              stop(event);
              onEnlarge(tool);
            }}
            onDoubleClick={stop}
            className="nodrag nopan group/mini relative flex h-full w-full cursor-zoom-in items-center overflow-hidden rounded-md border border-border bg-muted/40 px-1 py-0.5 outline-none hover:border-ring focus-visible:ring-2 focus-visible:ring-ring"
          >
            <DagMiniature graph={mini} steps={child?.steps} />
            <span
              data-testid="ve-dag-steps"
              className="pointer-events-none absolute bottom-0.5 right-1 rounded bg-card/80 px-1 text-[10px] leading-4 text-muted-foreground"
            >
              {summaryText}
            </span>
            <Maximize2
              aria-hidden="true"
              className="pointer-events-none absolute right-1 top-1 h-3 w-3 rounded bg-card/80 p-px text-muted-foreground opacity-0 group-hover/mini:opacity-100 group-focus-visible/mini:opacity-100"
            />
          </button>
        ) : (
          <MiniNote
            state={state === 'graph' || state === 'summary' ? 'loading' : state}
            problem={entry.problem}
            ts={ts}
          />
        )}
      </div>
      <InputsLine
        inputs={inputs}
        quiet={state !== 'graph' && state !== 'summary' && state !== 'empty'}
        height={parts.inputs}
        ts={ts}
      />
      <div style={{ height: parts.pad }} aria-hidden="true" />
    </div>
  );
}

/**
 * The DAGs card of a chat step or an agent step: the DAG tools of the step as
 * a small form block. The header has the slot name and the number of DAGs. Each
 * entry has the name of the DAG (the tool name when it differs), a line of
 * description, a miniature of the DAG that it calls, and a line with its
 * inputs: the ones that the model decides in the accent colour, the fixed ones
 * with a lock. The miniature loads only while the entry is on screen. A click
 * on the name opens the panel of the tool; a click on the miniature opens the
 * enlarged view of the DAG in the dock. A DAG that the server does not know is
 * red, with the reason in place of the miniature. In a run view the entry has the
 * status of the latest sub-run, a link to it, and the calls. The card lists
 * three DAGs, then a "+N more" row that opens the dock of the list; the add row
 * opens the dock that picks a tool. A compact canvas draws a tile (`SubTile`).
 * A step with DAG tools only shows the limits of its tool loop under the header.
 */
function DagCardNodeComponent({ data, selected }: NodeProps<StepFlowNode>) {
  const { node } = data;
  const card = node.slotCard;
  const { ts } = useI18n();
  const { compact = false, onCommand, selectedTool } = useCanvasActions();
  const run = useToolRun();
  if (!card) return null;
  const header = SLOT_CARD_HEADER;
  const height = SLOT_CARD_ROW;
  const shown = card.entries.slice(0, DAG_CARD_MAX_ENTRIES);
  const hidden = card.entries.length - shown.length;
  const openPanel = () =>
    onCommand({ kind: 'openSlotPanel', step: card.owner, slot: 'dags' });
  const open = (tool: ToolSelection) => onCommand({ kind: 'openTool', tool });
  const enlarge = (tool: ToolSelection) =>
    onCommand({ kind: 'openDagView', tool });
  const title = ts('DAGs');
  const addLabel = ts('Add a DAG');
  if (compact) {
    const used = totalCallsOf(run, card.stepName);
    return (
      <SubTile
        slot="dags"
        selected={
          selected ||
          (selectedTool?.step === card.owner &&
            selectedTool.category === 'dags')
        }
        icon={Workflow}
        label={title}
        count={card.entries.length}
        title={[
          title,
          ...card.entries.map((entry) => entry.label),
          ...(used ? [ts('Calls: {count}', { count: used })] : []),
        ].join('\n')}
        attrs={{
          'data-testid': `ve-canvas-node-${node.key}`,
          'data-shape': 'slotcard',
          'data-slot': 'dags',
        }}
        onOpen={openPanel}
      />
    );
  }
  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="slotcard"
      data-slot="dags"
      role="group"
      aria-label={title}
      aria-current={selected || undefined}
      data-selected={selected || undefined}
      // The card takes the focus: Enter or Space on it, as a double click on
      // the header or on the body outside an entry, opens the dock of the list.
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
      <SubHandle slot="dags" />
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
          {card.entries.length}
        </span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col py-px">
        {card.limits && (
          <ToolLimitsRow
            limits={card.limits}
            used={totalCallsOf(run, card.stepName)}
            height={height}
          />
        )}
        {shown.map((entry, index) => {
          const tool: ToolSelection = {
            step: card.owner,
            category: entry.category,
            index: entry.index,
          };
          return (
            <DagEntry
              key={`${entry.category}[${entry.index}]`}
              card={card}
              entry={entry}
              selected={sameTool(selectedTool, tool)}
              separated={index > 0 || card.limits !== undefined}
              onOpen={open}
              onEnlarge={enlarge}
            />
          );
        })}
        {hidden > 0 && (
          <div
            role="button"
            tabIndex={0}
            data-testid="ve-canvas-slot-more"
            aria-label={ts('+{count} more', { count: hidden })}
            style={{ height }}
            onClick={(event) => {
              event.stopPropagation();
              openPanel();
            }}
            onDoubleClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => {
              if (event.key !== 'Enter' && event.key !== ' ') return;
              event.preventDefault();
              event.stopPropagation();
              openPanel();
            }}
            className="flex w-full cursor-pointer items-center border-t border-border/60 px-2.5 text-[11px] text-muted-foreground outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          >
            {ts('+{count} more', { count: hidden })}
          </div>
        )}
        {card.editable && (
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
                slot: 'dags',
              });
            }}
            onDoubleClick={(event) => event.stopPropagation()}
            className="nodrag nopan flex w-full items-center gap-1.5 border-t border-border/60 px-2.5 text-left text-xs text-muted-foreground outline-none hover:bg-primary/10 hover:text-primary focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          >
            <Plus className="h-3.5 w-3.5" aria-hidden="true" />
            {addLabel}
          </button>
        )}
      </div>
    </div>
  );
}

export const DagCardNode = memo(DagCardNodeComponent);
