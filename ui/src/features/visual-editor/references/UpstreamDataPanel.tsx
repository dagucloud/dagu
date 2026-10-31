// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { Check, ChevronRight, CircleAlert, ListStart } from 'lucide-react';
import { NodeStatus, type components, type Status } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { PanelMessage } from '@/components/ui/panel-message';
import { RelativeTime } from '@/components/ui/relative-time';
import { Spinner } from '@/components/ui/spinner';
import { StatusDot } from '@/components/ui/status-dot';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import { useI18n } from '@/i18n/I18nProvider';
import { shortRunId } from '@/lib/shortRunId';
import { nodeStatusText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import { useStepOutputs } from '@/features/visual-editor/api/useStepOutputs';
import type {
  FollowedStepRun,
  InputRun,
  InputRunPicker,
  ReferenceScope,
  StepOutputs,
} from '../contracts';
import type { CacheEntry } from '../run/outputCache';
import {
  runInputProvenance,
  type InputProvenance,
} from '../run/inputProvenance';
import { findRunNode, formatOutputSize } from '../run/stepRunInfo';
import { useCaptureStepOutputs } from '../run/useCaptureStepOutputs';
import { useFollowedRun, type FollowedRunState } from '../run/useFollowedRun';
import {
  useCacheScope,
  useOutputCacheEntries,
} from '../run/useOutputCacheEntries';
import { upstreamDistances } from './candidates';
import { useStepLinks } from './StepLinks';
import { upstreamTree, type UpstreamNode } from './upstreamTree';
import { InputMark } from './InputMark';
import { RunSourcePicker } from '../run/RunSourcePicker';
import { JsonValue } from './JsonValue';

type ScopeStep = ReferenceScope['steps'][number];
const NAME_PATTERN = /^[A-Za-z][A-Za-z0-9_]*$/;

type OutputValue = StepOutputs['outputs'][number];

/** One output line: what the scope declares and what the run holds. */
interface OutputItem {
  /** Unique among the rows: an output variable can share a named output's name. */
  key: string;
  name: string;
  type?: string;
  source?: string;
  declared: boolean;
  /** Token to copy or insert. Null when the output cannot be referenced. */
  reference: string | null;
  run?: OutputValue;
}

/** A value that the browser cache holds, as the run would give it. */
function cachedValue(name: string, value: string): OutputValue {
  return { name, value, truncated: false, size: value.length };
}

/**
 * The output lines of a step. The cache entry gives the values only while no
 * run data is loaded, so the cache shows what the run can no longer give.
 */
function buildItems(
  step: ScopeStep,
  data: StepOutputs | undefined,
  entry?: CacheEntry,
  used?: Readonly<Record<string, string>>
): OutputItem[] {
  const held = entry?.values;
  // The values that the followed run used come before the run data.
  const usedValue = (name: string): OutputValue | undefined =>
    used !== undefined && name in used
      ? cachedValue(name, used[name] as string)
      : undefined;
  const heldValue = (name: string): OutputValue | undefined =>
    held !== undefined && name in held && !data
      ? cachedValue(name, held[name] as string)
      : undefined;
  // ${NAME} reads the output variable, ${steps.<id>.outputs.NAME} a named
  // output: a row reads the value of its own kind.
  const runValue = (name: string, variable: boolean) =>
    variable
      ? data?.outputVariable?.name === name
        ? data.outputVariable
        : undefined
      : data?.outputs.find((v) => v.name === name);
  const keyOf = (name: string, variable: boolean) =>
    variable ? `$${name}` : name;
  const items: OutputItem[] = step.outputs.map((o) => {
    const variable = o.source === 'outputVariable';
    return {
      key: keyOf(o.name, variable),
      name: o.name,
      type: o.type,
      source: o.source,
      declared: true,
      // Without an id, only an output variable (${NAME}) can be referenced.
      reference: step.id || variable ? o.reference : null,
      run: usedValue(o.name) ?? runValue(o.name, variable) ?? heldValue(o.name),
    };
  });
  const listed = new Set(items.map((i) => i.key));
  // `variable` is null for a cached value: the cache keeps no kind.
  const extra: { v: OutputValue; variable: boolean | null }[] = data
    ? [
        ...data.outputs.map((v) => ({ v, variable: false })),
        ...(data.outputVariable
          ? [{ v: data.outputVariable, variable: true }]
          : []),
      ]
    : [];
  // Values that only the cache holds (no run data): listed after the others.
  for (const name of Object.keys(used ?? {})) {
    if (!extra.some((e) => e.v.name === name)) {
      const v = usedValue(name);
      if (v) extra.push({ v, variable: null });
    }
  }
  for (const name of Object.keys(held ?? {})) {
    if (!extra.some((e) => e.v.name === name)) {
      const v = heldValue(name);
      if (v) extra.push({ v, variable: null });
    }
  }
  if (!data && !held && !used) return items;
  for (const { v, variable } of extra) {
    const key = keyOf(v.name, variable === true);
    if (
      listed.has(key) ||
      (variable === null && listed.has(keyOf(v.name, true)))
    ) {
      continue;
    }
    listed.add(key);
    // A runtime step has no declared outputs: build the token from its id.
    const buildable =
      variable !== true &&
      step.outputsKnown === 'runtime' &&
      !!step.id &&
      NAME_PATTERN.test(step.id) &&
      NAME_PATTERN.test(v.name);
    items.push({
      key,
      name: v.name,
      declared: false,
      reference: buildable ? `\${steps.${step.id}.outputs.${v.name}}` : null,
      run: v,
    });
  }
  return items;
}

/**
 * The values that the browser remembers of a step that a run reused, for
 * display when the run can no longer give them: the cache entry that read
 * them from the run that the record names. Nothing for a record that names
 * another run.
 */
export function usedValues(
  reused: InputProvenance | undefined,
  entry: CacheEntry | undefined
): Readonly<Record<string, string>> | undefined {
  if (!reused || !entry || !reused.runId) return undefined;
  return reused.runId === entry.runId ? entry.values : undefined;
}

type RunNode = components['schemas']['Node'];

/** What a row of the panel shows for its step in the followed run. */
export type UpstreamRowState =
  | 'waiting'
  | 'running'
  | 'success'
  | 'failed'
  | 'skipped'
  | 'notRun';

/** The state of a step in the followed run; `active` is true while the run goes on. */
export function upstreamRowState(
  node: RunNode | undefined,
  active: boolean
): UpstreamRowState {
  switch (node?.status) {
    case NodeStatus.Running:
    case NodeStatus.Retrying:
      return 'running';
    case NodeStatus.Success:
    case NodeStatus.PartialSuccess:
      return 'success';
    case NodeStatus.Failed:
    case NodeStatus.Aborted:
    case NodeStatus.Rejected:
      return 'failed';
    case NodeStatus.Skipped:
      return 'skipped';
    default:
      return active ? 'waiting' : 'notRun';
  }
}

/**
 * The run button of the panel, for "Run upstream steps" and "Run again".
 * Busy shows a spinner in the icon slot. A blocked button gives its reason in
 * a tooltip on a wrapper, as a disabled button takes no pointer event.
 */
function RunButton({
  label,
  busy,
  blocked,
  onClick,
}: {
  label: string;
  busy: boolean;
  blocked?: string;
  onClick(): void;
}): ReactElement {
  const button = (
    <Button
      type="button"
      variant="outline"
      size="xs"
      aria-busy={busy || undefined}
      disabled={blocked !== undefined}
      onClick={busy ? undefined : onClick}
    >
      {busy ? <Spinner /> : <ListStart aria-hidden="true" />}
      {label}
    </Button>
  );
  if (blocked === undefined) return button;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex shrink-0" tabIndex={0}>
          {button}
        </span>
      </TooltipTrigger>
      <TooltipContent side="bottom">{blocked}</TooltipContent>
    </Tooltip>
  );
}

/** The followed run, as one row of the panel reads it. */
interface RowFollow {
  runId: string;
  node?: RunNode;
  active: boolean;
}

const ROW_LABELS: Record<UpstreamRowState, string> = {
  waiting: 'Waiting',
  running: 'Running',
  success: 'Succeeded',
  failed: 'Failed',
  skipped: 'Skipped',
  notRun: 'Not run',
};

function RowStatus({
  state,
  name,
}: {
  state: UpstreamRowState;
  name: string;
}): ReactElement {
  const { ts } = useI18n();
  const label = ts(ROW_LABELS[state]);
  const icon =
    state === 'running' ? (
      <Spinner className="size-3 text-muted-foreground" />
    ) : state === 'success' ? (
      <Check className="h-3 w-3 text-success" aria-hidden="true" />
    ) : state === 'failed' ? (
      <CircleAlert className="h-3 w-3 text-destructive" aria-hidden="true" />
    ) : null;
  return (
    <span
      data-testid={`upstream-state-${name}`}
      data-state={state}
      role="status"
      className={cn(
        'ml-auto inline-flex items-center gap-1 text-[10px]',
        state === 'failed' ? 'text-destructive' : 'text-muted-foreground'
      )}
    >
      {icon}
      {label}
    </span>
  );
}

/** Puts a token in the field that has the focus. False when no field takes it. */
type InsertToken = (token: string) => boolean | void;

/**
 * A name that inserts its reference at the cursor of the field that has the
 * focus, or copies it when no field takes it. A copy shows a Check on the name
 * for 2 s.
 */
export function ReferenceName({
  name,
  reference,
  onInsert,
}: {
  name: string;
  reference: string | null;
  onInsert?: InsertToken;
}): ReactElement {
  const { ts } = useI18n();
  const { copied, copy } = useCopyFeedback(2000);
  if (!reference) return <span className="font-mono font-medium">{name}</span>;
  return (
    <button
      type="button"
      title={reference}
      aria-label={copied ? ts('Copied') : undefined}
      // A text field takes the plain text of a drop at the point of the drop.
      draggable
      onDragStart={(event) => {
        event.dataTransfer.setData('text/plain', reference);
        event.dataTransfer.effectAllowed = 'copy';
      }}
      onClick={() => {
        if (onInsert && onInsert(reference) !== false) return;
        void copy(reference);
      }}
      className="relative rounded font-mono font-medium hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {/* The name keeps its width, so the row does not move. */}
      <span className={cn(copied && 'invisible')}>{name}</span>
      {copied && (
        <Check
          className="absolute inset-0 m-auto size-3 text-success"
          aria-hidden="true"
        />
      )}
    </button>
  );
}

const SOURCE_LABELS: Record<string, string> = {
  declared: 'declared output',
  action: 'action output',
  stdoutField: 'stdout field',
  outputVariable: 'output variable',
};

/** The badge of an output source, in the language of the page. */
function sourceLabel(source: string, ts: (key: string) => string): string {
  const label = SOURCE_LABELS[source];
  return label ? ts(label) : source;
}

function OutputRow({
  item,
  hasRun,
  onInsert,
}: {
  item: OutputItem;
  hasRun: boolean;
  onInsert?: InsertToken;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <li className="rounded border border-border px-1.5 py-1">
      <div className="flex flex-wrap items-center gap-1">
        <ReferenceName
          name={item.name}
          reference={item.reference}
          onInsert={onInsert}
        />
        {item.type && (
          <span className="text-[10px] text-muted-foreground">{item.type}</span>
        )}
        {item.source && (
          <span className="text-[10px] text-muted-foreground">
            {sourceLabel(item.source, ts)}
          </span>
        )}
      </div>
      {item.run ? (
        <>
          <JsonValue value={item.run.value} />
          {item.run.truncated && (
            <p className="mt-0.5 text-[10px] text-warning">
              {ts('truncated ({size})', {
                size: formatOutputSize(item.run.size),
              })}
            </p>
          )}
        </>
      ) : (
        hasRun && (
          <p className="mt-0.5 text-muted-foreground">
            {ts('Not in this run')}
          </p>
        )
      )}
    </li>
  );
}

/** Pause between two reads of the outputs of a step that the run did not persist yet. */
export const OUTPUTS_RETRY_MS = 700;
/** Reads after the first one, at most, for one step of a followed run. */
export const OUTPUTS_RETRY_MAX = 12;

const UNFINISHED_NODE: ReadonlySet<NodeStatus> = new Set([
  NodeStatus.NotStarted,
  NodeStatus.Running,
  NodeStatus.Retrying,
  NodeStatus.Waiting,
]);

/**
 * True when a read of the outputs of a finished step is older than the run:
 * the live state of the run shows the step done before the stored state of
 * the run has the step done, with its outputs. The first step to finish gets
 * this read. It is never right to keep it.
 */
export function outputsLookStale(
  step: ScopeStep,
  data: StepOutputs | undefined
): boolean {
  if (!data) return false;
  if (UNFINISHED_NODE.has(data.status)) return true;
  if (data.status !== NodeStatus.Success) return false;
  const held = new Set(data.outputs.map((v) => v.name));
  if (data.outputVariable) held.add(data.outputVariable.name);
  return step.outputs.some((o) => !held.has(o.name));
}

/** A warning on a row: the step changed since the run, or the run has no output of it. */
type RowMark = { kind: 'stale' | 'missing'; runId: string };

function RowMarkNote({
  name,
  mark,
}: {
  name: string;
  mark: RowMark;
}): ReactElement {
  const { ts } = useI18n();
  const id = shortRunId(mark.runId);
  return (
    <span
      data-testid={`upstream-mark-${name}`}
      data-mark={mark.kind}
      className="inline-flex items-center gap-1 text-[10px] text-warning"
    >
      <span
        aria-hidden="true"
        className="inline-block size-1.5 shrink-0 rounded-full bg-warning"
      />
      {mark.kind === 'stale'
        ? ts('changed since run {id}', { id })
        : ts('no output in run {id}', { id })}
    </span>
  );
}

/**
 * The name of a previous step. With step links it is a button that opens the
 * step in the node view; it inserts nothing. Without them, plain text.
 */
function StepTitle({ name }: { name: string }): ReactElement {
  const { ts } = useI18n();
  const links = useStepLinks();
  if (!links) return <span className="font-mono font-medium">{name}</span>;
  return (
    <button
      type="button"
      aria-label={ts('Open step {name}', { name })}
      onClick={() => links.open(name)}
      className="rounded font-mono font-medium hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {name}
    </button>
  );
}

/** A step that the tree lists in full elsewhere: its name and a note. */
function RepeatedStep({ name }: { name: string }): ReactElement {
  const { ts } = useI18n();
  return (
    <div
      data-testid={`upstream-repeated-${name}`}
      className="flex items-baseline gap-1"
    >
      <StepTitle name={name} />
      <span className="text-[10px] text-muted-foreground">
        {ts('shown above')}
      </span>
    </div>
  );
}

function UpstreamStep({
  dagName,
  fileName,
  runId,
  step,
  follow,
  mark,
  cached,
  reused,
  onSelectRun,
  onInsert,
  collapsible = false,
  openByDefault = false,
  below,
}: {
  dagName: string;
  fileName?: string;
  runId: string | null;
  step: ScopeStep;
  follow?: RowFollow;
  mark?: RowMark;
  /** The fresh cached entry that covers the step: its run and capture time (ms). */
  cached?: { runId: string; capturedAt: number };
  /** The shown run did not execute the step: where its outputs came from. */
  reused?: InputProvenance;
  /** Selects the source run of the mark. */
  onSelectRun?: (runId: string) => void;
  onInsert?: InsertToken;
  /**
   * A chevron shows or hides the outputs of the step and the steps below it.
   * Without it, they always show.
   */
  collapsible?: boolean;
  /** The open state of a collapsible step until the user toggles it. */
  openByDefault?: boolean;
  /** The dependencies of the step, nested under it. */
  below?: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  const [toggled, setToggled] = useState<boolean | null>(null);
  const open = !collapsible || (toggled ?? openByDefault);
  const state = follow ? upstreamRowState(follow.node, follow.active) : null;
  // A followed run shows the outputs of a step once the step has finished.
  // A step that the run reused is read too: its node carries the outputs.
  // Otherwise a cached step shows the run that the next step run reads it
  // from, as its mark says.
  const outputsRunId = follow
    ? state === 'success' ||
      state === 'failed' ||
      (state === 'skipped' && reused !== undefined)
      ? follow.runId
      : null
    : (cached?.runId ?? runId);
  const result = useStepOutputs(dagName, outputsRunId, step.name);
  const active = outputsRunId !== null;
  const data = active ? result.data : undefined;
  // A followed run is read while it runs: a read that is older than the run
  // is read again, and a read is repeated when the run ends.
  const stale = active && !!follow && outputsLookStale(step, data);
  const { mutate } = result;
  // Counts the reads that the retry made; a read that gives the same data
  // keeps the same object, so the count also wakes the timer again.
  const [retries, setRetries] = useState(0);
  useEffect(() => {
    setRetries(0);
  }, [outputsRunId]);
  useEffect(() => {
    if (!stale || !mutate || retries >= OUTPUTS_RETRY_MAX) return;
    const timer = setTimeout(() => {
      void Promise.resolve(mutate()).finally(() => setRetries((n) => n + 1));
    }, OUTPUTS_RETRY_MS);
    return () => clearTimeout(timer);
  }, [stale, mutate, retries]);
  const runActive = follow?.active ?? false;
  const wasActive = useRef(runActive);
  const hadData = data !== undefined;
  useEffect(() => {
    if (wasActive.current && !runActive && hadData && mutate) void mutate();
    wasActive.current = runActive;
  }, [runActive, hadData, mutate]);
  const scope = useCacheScope(fileName ?? '');
  const entries = useOutputCacheEntries(scope);
  const entry = fileName ? entries[step.name] : undefined;
  const used = usedValues(reused, entry);
  // A reused step shows the values that the browser knows, so a failed read
  // is no reason to say that nothing loaded.
  const failed =
    active &&
    !!result.error &&
    !(reused !== undefined && used !== undefined && !data);
  const loading = active && !data && !failed && result.isLoading;
  // The outputs of a step that succeeded go to the cache, from the data that
  // this row already loaded. A read older than the run is not kept.
  useCaptureStepOutputs({
    fileName,
    dagName,
    runId: outputsRunId,
    step: step.name,
    data: stale ? undefined : data,
  });
  const items = buildItems(step, data, entry, used);
  return (
    <section
      data-testid={`upstream-item-${step.name}`}
      className="flex flex-col gap-1"
    >
      <div className="flex items-baseline gap-1">
        {collapsible && (
          <button
            type="button"
            aria-expanded={open}
            aria-label={ts('Outputs of {name}', { name: step.name })}
            onClick={() => setToggled(!open)}
            className="-ml-0.5 shrink-0 self-center rounded text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <ChevronRight
              aria-hidden="true"
              className={cn(
                'size-3.5 transition-transform',
                open && 'rotate-90'
              )}
            />
          </button>
        )}
        <StepTitle name={step.name} />
        {step.id && step.id !== step.name && (
          <span className="font-mono text-[10px] text-muted-foreground">
            ({step.id})
          </span>
        )}
        {data && !follow && !reused && (
          <span className="inline-flex items-center gap-1 text-[10px] text-muted-foreground">
            <StatusDot kind="step" status={data.status} decorative />
            {ts(nodeStatusText(data.status))}
          </span>
        )}
        {reused && (
          <InputMark
            provenance={reused}
            variant="plain"
            onSelectRun={onSelectRun}
            testId={`upstream-reused-${step.name}`}
          />
        )}
        {mark && !reused && <RowMarkNote name={step.name} mark={mark} />}
        {cached !== undefined && !reused && (
          <InputMark
            provenance={{
              step: step.name,
              source: 'cache',
              runId: cached.runId,
              stale: false,
              capturedAt: cached.capturedAt,
            }}
            variant="plain"
            onSelectRun={onSelectRun}
            testId={`upstream-cached-${step.name}`}
          />
        )}
        {state && !reused && <RowStatus state={state} name={step.name} />}
        {loading && (
          <span className="text-[10px] text-muted-foreground">
            {ts('Loading outputs...')}
          </span>
        )}
      </div>
      {/* The body stays mounted when it is closed: the outputs are read and kept in the cache as before. */}
      <div
        hidden={!open}
        className={cn('flex-col gap-1', open ? 'flex' : 'hidden')}
      >
        {!step.id && (
          <PanelMessage tone="muted" align="start" className="p-0">
            {ts('Add an id to this step to reference its outputs')}
          </PanelMessage>
        )}
        {state === 'failed' && follow?.node?.error && (
          <p
            data-testid={`upstream-error-${step.name}`}
            className="max-h-24 overflow-auto whitespace-pre-wrap break-words rounded border border-destructive/40 bg-destructive/10 px-1.5 py-1 text-destructive"
          >
            {follow.node.error}
          </p>
        )}
        {failed && (
          <PanelMessage tone="error" align="start" className="p-0">
            {ts('Could not load {thing}', {
              thing: ts('the outputs of this step'),
            })}
          </PanelMessage>
        )}
        {items.length === 0 && !failed && !loading && (
          <PanelMessage tone="muted" align="start" className="p-0">
            {data ? ts('No outputs in this run') : ts('No declared outputs')}
          </PanelMessage>
        )}
        <ul className="flex flex-col gap-1">
          {items.map((item) => (
            <OutputRow
              key={item.key}
              item={item}
              hasRun={!!data}
              onInsert={onInsert}
            />
          ))}
        </ul>
      </div>
      {below && (
        <div
          hidden={!open}
          data-testid={`upstream-below-${step.name}`}
          className={cn(
            'ml-1.5 flex-col gap-2 border-l border-border pl-2',
            open ? 'flex' : 'hidden'
          )}
        >
          {below}
        </div>
      )}
    </section>
  );
}

/**
 * The run that feeds the step: its status, its short id, which selects the
 * run, and its age. With no run, it tells which active run the step waits for.
 */
function InputRunHeader({
  runId,
  inputRun,
  status,
  startedAt,
  usedCache,
  picker,
}: {
  runId: string | null;
  inputRun: InputRun;
  status?: Status;
  startedAt?: string;
  /**
   * The shown run reused cached values (true) or did not (false).
   * Absent: the cache that would cover the next run decides.
   */
  usedCache?: boolean;
  /** The run that the next step run takes its inputs from: a choice. */
  picker?: InputRunPicker;
}): ReactElement | null {
  const { ts } = useI18n();
  const cache = inputRun.cache;
  const cacheUsed = usedCache ?? (!!cache && cache.cached.size > 0);
  if (picker && picker.options.length > 0) {
    return (
      <div
        data-testid="upstream-input-run"
        data-run-id={runId ?? undefined}
        className="flex min-w-0 flex-wrap items-center gap-1 text-[10px] text-muted-foreground"
      >
        <span>{ts('Inputs from')}</span>
        <RunSourcePicker
          options={picker.options}
          selectedId={picker.selectedId}
          onSelect={picker.onSelect}
          className="min-w-0"
        />
      </div>
    );
  }
  if (runId === null) {
    if (cache && cacheUsed && cache.capturedAt !== null) {
      // The time is a part of the sentence: split the text at its place.
      const [first = '', last = ''] = ts('Inputs from your cache ({time})', {
        time: '\u0000',
      }).split('\u0000');
      return (
        <p
          data-testid="upstream-input-cache"
          className="text-[10px] text-muted-foreground"
        >
          {first}
          <RelativeTime
            timestamp={new Date(cache.capturedAt).toISOString()}
            compact
            ago
          />
          {last}
        </p>
      );
    }
    return inputRun.waitingFor !== null ? (
      <p
        data-testid="upstream-input-waiting"
        className="text-[10px] text-muted-foreground"
      >
        {ts('Waits for run {id} to finish', {
          id: shortRunId(inputRun.waitingFor),
        })}
      </p>
    ) : null;
  }
  // The id is a button inside the sentence: split the text at its place.
  const [before = '', after = ''] = ts(
    cacheUsed ? 'Inputs from run {id} and your cache' : 'Inputs from run {id}',
    { id: '\u0000' }
  ).split('\u0000');
  const id = (
    <span className="font-mono text-foreground">{shortRunId(runId)}</span>
  );
  return (
    <div
      data-testid="upstream-input-run"
      data-run-id={runId}
      className="flex items-center gap-1 text-[10px] text-muted-foreground"
    >
      {status !== undefined && (
        <StatusDot kind="run" status={status} decorative />
      )}
      <span>{before}</span>
      {inputRun.onSelectRun ? (
        <button
          type="button"
          title={runId}
          className="rounded-sm underline underline-offset-2 hover:text-primary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          onClick={() => inputRun.onSelectRun?.(runId)}
        >
          {id}
        </button>
      ) : (
        <span title={runId}>{id}</span>
      )}
      <span>{after}</span>
      {startedAt && <RelativeTime timestamp={startedAt} compact ago />}
    </div>
  );
}

const NO_REUSED: ReadonlyMap<string, InputProvenance> = new Map();

type ScopeVariable = ReferenceScope['params'][number];

/** The params and consts of the workflow, for a step with no upstream step. */
function WorkflowVariables({
  scope,
  onInsert,
}: {
  scope: ReferenceScope;
  onInsert?: InsertToken;
}): ReactElement | null {
  const { ts } = useI18n();
  const groups: { title: string; items: ScopeVariable[] }[] = [
    { title: ts('Parameters'), items: scope.params ?? [] },
    { title: ts('Constants'), items: scope.consts ?? [] },
  ].filter((group) => group.items.length > 0);
  if (groups.length === 0) return null;
  return (
    <div className="flex flex-col gap-2" data-testid="upstream-variables">
      {groups.map((group) => (
        <div key={group.title} className="flex flex-col gap-1">
          <h4 className="text-xs font-semibold text-muted-foreground">
            {group.title}
          </h4>
          <ul className="flex flex-col gap-1">
            {group.items.map((variable) => (
              <li
                key={variable.reference}
                className="flex flex-wrap items-center gap-1 rounded border border-border px-1.5 py-1"
              >
                <ReferenceName
                  name={variable.name}
                  reference={variable.reference}
                  onInsert={onInsert}
                />
                {variable.defaultValue && (
                  <span className="text-[10px] text-muted-foreground">
                    = {variable.defaultValue}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

/**
 * Where the panel shows: 'bottom' under the form of a step, or 'column' as
 * the full height of a column of its own.
 */
export type UpstreamDataLayout = 'bottom' | 'column';

/** The panel props that the wrapper reads. */
interface PanelProps {
  dagName: string;
  /**
   * The workflow file. With it, the panel keeps the outputs that it reads in
   * the browser cache, each with the run that it was read from.
   */
  fileName?: string;
  runId: string | null;
  scope?: ReferenceScope;
  stepName: string | null;
  /** Gives false when no field can take the token. */
  onInsert?: InsertToken;
  /**
   * Runs the upstream steps of the step, not the step. The button shows
   * only with this callback and when the step has upstream steps.
   */
  onRunUpstream?(): void;
  /** Why the run is not possible now. The button shows and is disabled. */
  runUpstreamBlocked?: string;
  /**
   * The run that the panel follows: the run of the upstream steps, or the run
   * of the step itself when no upstream step ran in it. A run of another
   * step is ignored.
   */
  followedRun?: FollowedStepRun | null;
  /**
   * The run that feeds the step, with the steps that changed since it or
   * hold no output in it. The header shows the run, and a row shows a mark.
   * Without it, the header is the plain "Run {runId}" line.
   */
  inputRun?: InputRun;
  layout?: UpstreamDataLayout;
}

/**
 * Lists the upstream steps of a step by distance, with their outputs and
 * last-run values. With a followed run, each row shows the state of its step
 * in that run, and then the outputs that the run made.
 */
export function UpstreamDataPanel(props: PanelProps): ReactElement {
  const { followedRun, stepName, dagName } = props;
  const followed =
    followedRun && followedRun.step === stepName ? followedRun : null;
  // The live run is read only while a run is followed.
  return followed ? (
    <FollowedPanel {...props} followed={followed} dagName={dagName} />
  ) : (
    <PanelView {...props} followed={null} live={null} />
  );
}

function FollowedPanel({
  followed,
  ...props
}: PanelProps & { followed: FollowedStepRun }): ReactElement {
  const live = useFollowedRun(props.dagName, followed.runId);
  return <PanelView {...props} followed={followed} live={live} />;
}

function PanelView({
  dagName,
  fileName,
  runId,
  scope,
  stepName,
  onInsert,
  onRunUpstream,
  runUpstreamBlocked,
  followed,
  live,
  inputRun,
  layout = 'bottom',
}: Omit<PanelProps, 'followedRun'> & {
  followed: FollowedStepRun | null;
  live: FollowedRunState | null;
}): ReactElement {
  const { ts } = useI18n();
  // The steps that the shown run reused. The followed entry knows them at
  // once; the labels of the run give them again after a reload.
  const liveDetails = live?.details ?? undefined;
  const reusedSteps = useMemo((): ReadonlyMap<string, InputProvenance> => {
    if (!followed) return inputRun?.provenance ?? NO_REUSED;
    const own = new Map<string, InputProvenance>(
      (followed.reused ?? []).map((r) => [r.step, r])
    );
    for (const [name, record] of runInputProvenance(liveDetails)) {
      if (!own.has(name)) own.set(name, record);
    }
    return own;
  }, [followed, inputRun?.provenance, liveDetails]);
  // The id of a source run is a button when the history holds that run.
  const selectRunFor = (
    record: InputProvenance | undefined
  ): ((runId: string) => void) | undefined => {
    const select = inputRun?.onSelectRun;
    const resolve = inputRun?.resolveRun;
    if (!select || !record?.runId) return undefined;
    const id = resolve ? resolve(record.runId) : record.runId;
    return id === null ? undefined : () => select(id);
  };
  const followOf = (name: string): RowFollow | undefined =>
    followed && live
      ? {
          runId: followed.runId,
          node: findRunNode(live.details ?? undefined, name),
          active: live.active,
        }
      : undefined;
  // The step has a fresh cached entry: the run that it was read from and when.
  const cachedOf = (
    name: string
  ): { runId: string; capturedAt: number } | undefined => {
    const cache = inputRun?.cache;
    const time = cache?.times[name];
    const from = cache?.runs[name];
    return cache?.cached.has(name) && time !== undefined && from !== undefined
      ? { runId: from, capturedAt: time }
      : undefined;
  };
  const cachedProvenance = (name: string): InputProvenance | undefined => {
    const held = cachedOf(name);
    return held
      ? {
          step: name,
          source: 'cache',
          runId: held.runId,
          stale: false,
          capturedAt: held.capturedAt,
        }
      : undefined;
  };
  // The marks tell of the run that the header shows.
  const markOf = (name: string): RowMark | undefined => {
    if (!inputRun || runId === null) return undefined;
    if (inputRun.stale.has(name)) return { kind: 'stale', runId };
    if (inputRun.missing.has(name)) return { kind: 'missing', runId };
    return undefined;
  };
  // The run of the upstream steps goes on: both run buttons show it.
  const upstreamRun = followed?.kind === 'upstream';
  const busy = upstreamRun && !!live?.active;
  const hint = (text: string) => (
    <PanelMessage tone="muted" align="start" className="p-0">
      {text}
    </PanelMessage>
  );

  const hasUpstream =
    stepName !== null &&
    !!scope?.steps.some((s) => s.name === stepName) &&
    upstreamDistances(scope, stepName).size > 0;

  let body: ReactElement;
  if (stepName === null) {
    body = hint(ts('Select a step to see the data of its upstream steps'));
  } else if (!scope) {
    body = hint(ts('No reference data yet'));
  } else if (!scope.steps.some((s) => s.name === stepName)) {
    // The scope is the last valid one: a draft step is not in it yet.
    body = hint(ts('Save or fix the step to see its upstream data'));
  } else {
    const distances = upstreamDistances(scope, stepName);
    if (distances.size === 0) {
      body = (
        <div className="flex flex-col gap-2">
          {hint(ts('This step has no upstream steps'))}
          <WorkflowVariables scope={scope} onInsert={onInsert} />
        </div>
      );
    } else {
      // The dependency tree: the direct dependencies show their outputs, the
      // steps under them are closed until the user opens them. A followed run
      // shows every step, so the user sees the run go.
      const renderNode = (node: UpstreamNode, depth: number): ReactElement => {
        const { step } = node;
        if (node.repeated) {
          return <RepeatedStep key={step.name} name={step.name} />;
        }
        return (
          <UpstreamStep
            key={step.name}
            dagName={dagName}
            {...(fileName !== undefined ? { fileName } : {})}
            runId={runId}
            step={step}
            follow={followOf(step.name)}
            mark={markOf(step.name)}
            cached={cachedOf(step.name)}
            reused={reusedSteps.get(step.name)}
            onSelectRun={selectRunFor(
              reusedSteps.get(step.name) ?? cachedProvenance(step.name)
            )}
            onInsert={onInsert}
            collapsible={depth > 0}
            openByDefault={!!followed}
            below={
              node.children.length > 0
                ? node.children.map((child) => renderNode(child, depth + 1))
                : undefined
            }
          />
        );
      };
      body = (
        <div className="flex flex-col gap-1">
          <h4 className="text-xs font-semibold text-muted-foreground">
            {ts('Direct upstream')}
          </h4>
          <div className="flex flex-col gap-2">
            {upstreamTree(scope, stepName).map((node) => renderNode(node, 0))}
          </div>
        </div>
      );
    }
  }

  return (
    <div
      data-layout={layout}
      className={cn(
        'flex flex-col gap-2 text-xs',
        layout === 'column' && 'h-full min-h-0 flex-1 overflow-y-auto p-2'
      )}
    >
      <div className="flex flex-col gap-0.5">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-xs font-semibold">{ts('Upstream data')}</h3>
          {onRunUpstream && hasUpstream && (
            <RunButton
              label={ts('Run upstream steps')}
              busy={busy}
              blocked={runUpstreamBlocked}
              onClick={onRunUpstream}
            />
          )}
        </div>
        {inputRun ? (
          <InputRunHeader
            runId={runId}
            inputRun={inputRun}
            usedCache={
              followed
                ? [...reusedSteps.values()].some((r) => r.source === 'cache')
                : undefined
            }
            picker={followed ? undefined : inputRun.picker}
            status={
              followed && live?.details
                ? live.details.status
                : inputRun.runId === runId
                  ? inputRun.status
                  : undefined
            }
            startedAt={
              followed
                ? new Date(followed.startedAt).toISOString()
                : inputRun.runId === runId
                  ? inputRun.startedAt
                  : undefined
            }
          />
        ) : (
          runId !== null && (
            <p className="text-[10px] text-muted-foreground">
              {ts('Run {runId}', { runId })}
            </p>
          )
        )}
        {upstreamRun && followed && (
          <div
            data-testid="upstream-last-run"
            className="flex items-center justify-between gap-2 text-[10px] text-muted-foreground"
          >
            <span>
              {ts('Last run')}{' '}
              <RelativeTime
                timestamp={new Date(followed.startedAt).toISOString()}
                compact
                ago
              />
            </span>
            {onRunUpstream && hasUpstream && (
              <RunButton
                label={ts('Run again')}
                busy={busy}
                blocked={runUpstreamBlocked}
                onClick={onRunUpstream}
              />
            )}
          </div>
        )}
        {followed && live?.error && !live.details && (
          <PanelMessage tone="error" align="start" className="p-0 text-[10px]">
            {ts('Could not load {thing}', {
              thing: ts('the run {runId}', { runId: followed.runId }),
            })}
          </PanelMessage>
        )}
      </div>
      {body}
    </div>
  );
}
