// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { stringify } from 'yaml';
import type { ValidationIssue } from '../contracts';
import { issuesUnder } from '../inspector/issues';
import { builtinHint, builtinLabel } from './builtin';
import {
  SCOPE_KINDS,
  kindBadge,
  kindGroupTitle,
  kindLabel,
  kindOfCategory,
  stepAccepts,
  type DockScope,
  type ToolKind,
  type ToolStepKind,
} from './kinds';
import {
  BUILTIN_TOOLS,
  DEFAULT_MAX_TOOLS,
  DEFAULT_MAX_TOOL_CALLS,
  DEFAULT_TOOL_RESULT_MAX_BYTES,
  entryKey,
  entryPath,
  toolNameOf,
  type BuiltinEntryModel,
  type BuiltinToolName,
  type ToolEntryModel,
} from './model';
import { namedMcpEntry, NEW_MCP_ENTRY, type ToolPick } from './pick';
import type { DagCatalogItem, DagInputDef } from './useDagCatalog';

type Translate = (
  text: string,
  params?: Record<string, string | number>
) => string;

/** Where a row comes from: the tools of the step, or what the catalogue can add. */
export type RowGroup = 'step' | 'catalog';

/** One row of the Tools dock: a tool of the step, or a tool that the step can add. */
export interface DockRow {
  /** The identity of the row: `step:dags[0]`, `step:builtin:shell`, `catalog:dag:lookup`. */
  id: string;
  group: RowGroup;
  kind: ToolKind;
  title: string;
  /** One line. Empty when the tool has no description. */
  description: string;
  /** A short mono text after the title: the tool name that the model sees. */
  tag: string;
  /** The issues of the entry, with the issues of every field below it. */
  issues: ValidationIssue[];
  /** A tool entry of the step (`group` is `step`, not a built-in tool). */
  entry?: ToolEntryModel;
  /** A built-in tool that the step turns on. */
  builtin?: BuiltinEntryModel;
  /** What a catalogue row adds. */
  pick?: ToolPick;
  /** True for the catalogue row of a new inline MCP server. */
  fresh?: boolean;
  /** True when the DAG of the row is known and has no description. */
  blank?: boolean;
  /** True when the DAGs directory is known in full and has no such DAG. */
  unknown?: boolean;
}

/** Why a section of the catalogue has no row, for the note that it shows. */
export type EmptyReason =
  | 'no-dags'
  | 'all-dags'
  | 'no-servers'
  | 'all-servers'
  | 'all-builtin'
  | 'loading';

export interface DockSection {
  /** `step:dag`, `catalog:mcp`... */
  id: string;
  group: RowGroup;
  kind: ToolKind;
  title: string;
  rows: DockRow[];
  /** Set on a catalogue section with no row and no search. */
  empty?: EmptyReason;
}

/** The lower-case words of a search text. */
export function queryWords(query: string): string[] {
  return query.toLowerCase().split(/\s+/).filter(Boolean);
}

/** True when every word of the query is in one of the texts. */
export function matchesWords(
  words: readonly string[],
  texts: readonly string[]
): boolean {
  if (words.length === 0) return true;
  const haystack = texts.join('\n').toLowerCase();
  return words.every((word) => haystack.includes(word));
}

/** The texts that a search tests for a row. */
export function rowTexts(ts: Translate, row: DockRow): string[] {
  const texts = [
    row.title,
    row.description,
    row.tag,
    kindLabel(ts, row.kind),
    kindBadge(ts, row.kind),
  ];
  const entry = row.entry;
  if (entry) {
    texts.push(...entry.params.map((param) => param.name));
    if (entry.mcp) texts.push(entry.mcp.url, ...entry.mcp.allow);
  }
  if (row.builtin) {
    texts.push(...(row.builtin.allow ?? []), ...(row.builtin.deny ?? []));
  }
  return texts;
}

/** A rank for a catalogue search: an exact name first, then a name that starts with the text. */
function rank(words: readonly string[], row: DockRow): number {
  if (words.length === 0) return 0;
  const name = row.title.toLowerCase();
  const text = words.join(' ');
  if (name === text) return 0;
  if (name.startsWith(text)) return 1;
  if (words.every((word) => name.includes(word))) return 2;
  return 3;
}

function entryTitle(ts: Translate, entry: ToolEntryModel): string {
  if (entry.kind === 'invalid') return ts('Invalid tool entry');
  return entry.label || entry.mcp?.host || kindLabel(ts, entry.kind);
}

function entryDescription(
  ts: Translate,
  entry: ToolEntryModel,
  dags: ReadonlyMap<string, DagCatalogItem>
): string {
  switch (entry.kind) {
    case 'invalid':
      return entry.problem ? ts(entry.problem) : '';
    case 'dag':
      return dags.get(entry.label)?.description ?? '';
    case 'mcp':
      if (entry.mcp?.named) return ts('Server entry of the AI page');
      return entry.mcp?.url
        ? entry.mcp.url
        : ts('Not set up: give it a name and a URL.');
  }
}

/** The tag of a DAG tool: its name for the model, when it differs from the DAG name. */
function toolTag(entry: ToolEntryModel): string {
  if (entry.kind !== 'dag' || entry.label === '') return '';
  const name = toolNameOf(entry.label);
  return name === entry.label ? '' : name;
}

/** The rows of the entries of the step, in the order of the file. */
export function stepEntryRows(
  ts: Translate,
  entries: readonly ToolEntryModel[],
  dags: ReadonlyMap<string, DagCatalogItem>,
  fieldIssues: Record<string, ValidationIssue[]>,
  complete = false
): DockRow[] {
  return entries.map((entry) => ({
    id: `step:${entryKey(entry)}`,
    group: 'step',
    // An entry that is not a mapping stays in the list of its category.
    kind: kindOfCategory(entry.category),
    title: entryTitle(ts, entry),
    description: entryDescription(ts, entry, dags),
    tag: toolTag(entry),
    issues: issuesUnder(fieldIssues, entryPath(entry)),
    entry,
    ...(entry.kind === 'dag' && dags.get(entry.label)?.description === ''
      ? { blank: true as const }
      : {}),
    // A name that a `${...}` reference gives at run time is no DAG name yet.
    ...(entry.kind === 'dag' &&
    complete &&
    entry.label !== '' &&
    !entry.label.includes('${') &&
    !dags.has(entry.label)
      ? { unknown: true as const }
      : {}),
  }));
}

/** The rows of the built-in tools that the step turns on. */
export function stepBuiltinRows(
  ts: Translate,
  builtin: readonly BuiltinEntryModel[],
  fieldIssues: Record<string, ValidationIssue[]>
): DockRow[] {
  return builtin.map((tool) => ({
    id: `step:builtin:${tool.name}`,
    group: 'step',
    kind: 'builtin',
    title: builtinLabel(ts, tool.name),
    description: builtinHint(ts, tool.name),
    tag: '',
    issues: issuesUnder(fieldIssues, `with.tools.builtin.${tool.name}`),
    builtin: tool,
  }));
}

export interface ToolSectionsInput {
  ts: Translate;
  step: ToolStepKind;
  /** The dock: the DAG tools (`dags`), or the MCP servers and built-in tools (`tools`). */
  scope: DockScope;
  query: string;
  entries: readonly ToolEntryModel[];
  /** The built-in tools that the step turns on. */
  builtin: readonly BuiltinEntryModel[];
  dags: readonly DagCatalogItem[];
  dagsLoading: boolean;
  /** True when `dags` holds every DAG of the directory. */
  dagsComplete?: boolean;
  /** The names of the MCP server entries that the DAG may use. */
  servers: readonly string[];
  /** The DAG that holds the step: it is no tool of itself. */
  exclude?: string | undefined;
  fieldIssues: Record<string, ValidationIssue[]>;
  /** False for a view that only reads: no catalogue. */
  canAdd: boolean;
}

function section(
  ts: Translate,
  group: RowGroup,
  kind: ToolKind,
  rows: DockRow[],
  empty?: EmptyReason
): DockSection {
  return {
    id: `${group}:${kind}`,
    group,
    kind,
    title: kindGroupTitle(ts, kind),
    rows,
    ...(empty ? { empty } : {}),
  };
}

/**
 * The sections of a dock of a step. The DAGs dock has the DAG tools of the
 * step and the DAGs of the DAGs directory. The Tools dock has the MCP servers
 * and the built-in tools: no DAG shows in it. In both, the tools of the step
 * come first, one section for each kind that has a tool. The catalogue
 * follows, when the dock can add: the DAGs of the DAGs directory, or the MCP
 * server entries of the AI page, a new inline server and the built-in tools
 * that the step does not have. A search keeps the rows that match and drops
 * the sections with no row. Without a search, a catalogue section with no row
 * stays with the reason.
 */
export function buildToolSections(input: ToolSectionsInput): DockSection[] {
  const { ts, step, scope, entries, builtin, fieldIssues } = input;
  const kinds = SCOPE_KINDS[scope];
  const words = queryWords(input.query);
  const searching = words.length > 0;
  const byName = new Map(input.dags.map((dag) => [dag.name, dag]));
  const keep = (rows: DockRow[]) =>
    rows.filter((row) => matchesWords(words, rowTexts(ts, row)));
  const all = stepEntryRows(
    ts,
    entries,
    byName,
    fieldIssues,
    input.dagsComplete === true && !input.dagsLoading
  );
  const sections: DockSection[] = [];
  const push = (value: DockSection) => {
    if (value.rows.length > 0 || (!searching && value.empty)) {
      sections.push(value);
    }
  };

  if (kinds.includes('dag')) {
    push(
      section(ts, 'step', 'dag', keep(all.filter((row) => row.kind === 'dag')))
    );
  }
  if (kinds.includes('mcp')) {
    push(
      section(ts, 'step', 'mcp', keep(all.filter((row) => row.kind === 'mcp')))
    );
  }
  if (kinds.includes('builtin') && stepAccepts(step, 'builtin')) {
    push(
      section(
        ts,
        'step',
        'builtin',
        keep(stepBuiltinRows(ts, builtin, fieldIssues))
      )
    );
  }
  if (!input.canAdd) return sections;

  if (kinds.includes('dag')) {
    const used = new Set(
      entries.filter((e) => e.kind === 'dag').map((e) => e.label)
    );
    const others = input.dags.filter((dag) => dag.name !== input.exclude);
    // A DAG that the step has already is not an item to add: two entries with the
    // same name are a validation error.
    const listed = others.filter((dag) => !used.has(dag.name));
    const dagRows: DockRow[] = listed.map((dag) => ({
      id: `catalog:dag:${dag.name}`,
      group: 'catalog',
      kind: 'dag',
      title: dag.name,
      description: dag.description,
      tag: '',
      issues: [],
      pick: { category: 'dags', entry: { name: dag.name } },
      ...(dag.description === '' ? { blank: true as const } : {}),
    }));
    const dagMatches = keep(dagRows)
      .map((row, index) => ({ row, index }))
      .sort(
        (a, b) => rank(words, a.row) - rank(words, b.row) || a.index - b.index
      )
      .map(({ row }) => row);
    push(
      section(
        ts,
        'catalog',
        'dag',
        dagMatches,
        listed.length > 0
          ? undefined
          : input.dagsLoading
            ? 'loading'
            : others.length === 0
              ? 'no-dags'
              : 'all-dags'
      )
    );
  }

  if (kinds.includes('mcp')) {
    const usedServers = new Set(
      entries.filter((e) => e.mcp?.named).map((e) => e.label)
    );
    const freeServers = input.servers.filter((name) => !usedServers.has(name));
    const serverRows: DockRow[] = freeServers.map((name) => ({
      id: `catalog:mcp:${name}`,
      group: 'catalog',
      kind: 'mcp',
      title: name,
      description: ts('Server entry of the AI page'),
      tag: '',
      issues: [],
      pick: { category: 'mcp', entry: namedMcpEntry(name) },
    }));
    const fresh: DockRow = {
      id: 'catalog:mcp:new',
      group: 'catalog',
      kind: 'mcp',
      title: ts('New MCP server with a URL'),
      description: ts('Connect a remote MCP server by its URL.'),
      tag: '',
      issues: [],
      pick: { category: 'mcp', entry: NEW_MCP_ENTRY },
      fresh: true,
    };
    const mcpRows = keep([...serverRows, fresh]);
    push(
      section(
        ts,
        'catalog',
        'mcp',
        mcpRows,
        freeServers.length > 0
          ? undefined
          : input.servers.length === 0
            ? 'no-servers'
            : 'all-servers'
      )
    );
  }

  if (kinds.includes('builtin') && stepAccepts(step, 'builtin')) {
    const taken = new Set(builtin.map((tool) => tool.name));
    const free = BUILTIN_TOOLS.filter((name) => !taken.has(name));
    const rows: DockRow[] = free.map((name: BuiltinToolName) => ({
      id: `catalog:builtin:${name}`,
      group: 'catalog',
      kind: 'builtin',
      title: builtinLabel(ts, name),
      description: builtinHint(ts, name),
      tag: '',
      issues: [],
      pick: { category: 'builtin', tool: name },
    }));
    push(
      section(
        ts,
        'catalog',
        'builtin',
        keep(rows),
        free.length === 0 ? 'all-builtin' : undefined
      )
    );
  }
  return sections;
}

/** A small fact that a row shows under its description. */
export interface RowChip {
  id: string;
  text: string;
  tone: 'default' | 'warning';
}

const SHOWN_NAMES = 3;

function namesText(ts: Translate, names: readonly string[]): string {
  const shown = names.slice(0, SHOWN_NAMES).join(', ');
  const more = names.length - SHOWN_NAMES;
  return more > 0
    ? ts('{names} and {count} more', { names: shown, count: more })
    : shown;
}

/**
 * The facts of a row of the step: the fixed inputs and the inputs of the model
 * of a DAG tool, the allow list of an MCP server, the allow and deny rules of
 * the shell tool. A fact that makes the tool useless is a warning.
 */
export function rowChips(ts: Translate, row: DockRow): RowChip[] {
  const chips: RowChip[] = [];
  const entry = row.entry;
  if (entry?.kind === 'dag') {
    const fixed = entry.params.filter((p) => p.state === 'fixed').length;
    const model = entry.params.filter((p) => p.state === 'from_ai').length;
    if (row.unknown) {
      chips.push({
        id: 'unknown',
        text: ts('Not in the DAGs directory'),
        tone: 'warning',
      });
    }
    if (fixed > 0) {
      chips.push({
        id: 'fixed',
        text: ts('{count} fixed', { count: fixed }),
        tone: 'default',
      });
    }
    if (model > 0) {
      chips.push({
        id: 'model',
        text: ts('{count} from the model', { count: model }),
        tone: 'default',
      });
    }
  } else if (entry?.kind === 'mcp') {
    const allow = entry.mcp?.allow ?? [];
    chips.push(
      allow.length > 0
        ? {
            id: 'allow',
            text: ts('Allow: {names}', { names: namesText(ts, allow) }),
            tone: 'default',
          }
        : { id: 'allow', text: ts('No allowed tools'), tone: 'warning' }
    );
  } else if (row.builtin?.name === 'shell') {
    const allow = row.builtin.allow ?? [];
    const deny = row.builtin.deny ?? [];
    chips.push(
      allow.length > 0
        ? {
            id: 'allow',
            text: ts('Allow: {names}', { names: namesText(ts, allow) }),
            tone: 'default',
          }
        : { id: 'allow', text: ts('No allowed commands'), tone: 'warning' }
    );
    if (deny.length > 0) {
      chips.push({
        id: 'deny',
        text: ts('Deny: {names}', { names: namesText(ts, deny) }),
        tone: 'default',
      });
    }
  }
  return chips;
}

/** The row at `index` of the sections, in the order of the screen, or undefined. */
export function flatRows(sections: readonly DockSection[]): DockRow[] {
  return sections.flatMap((item) => item.rows);
}

/** Who decides the value of one input of a DAG tool. */
export type InputState = 'fixed' | 'from_ai' | 'open';

/**
 * The classes of the chip of an input state, in the tool detail and in the
 * DAGs card: fixed in the text colour, from the model in the accent, open
 * muted. Literal strings, so that Tailwind finds them.
 */
export const INPUT_STATE_CHIP: Record<InputState, string> = {
  fixed: 'bg-muted text-foreground',
  from_ai: 'bg-primary/10 text-primary',
  open: 'bg-muted/60 text-muted-foreground',
};

/** One line of the inputs table of a DAG tool. */
export interface InputRow {
  name: string;
  /** The type that the DAG declares. Empty for an input that the DAG does not declare. */
  type: string;
  required: boolean;
  /** What the DAG says about the input. */
  description: string;
  state: InputState;
  /** The fixed value, as text. Only for `fixed`. */
  value?: string;
  /** The text for the model of `from_ai: {description}`. Only for `from_ai`. */
  modelText?: string;
  /** The default of the DAG, as text. */
  fallback: string;
  /** True when the entry names an input that the DAG does not declare. */
  undeclared: boolean;
}

function valueText(value: unknown): string {
  if (typeof value === 'string') return value;
  const json = JSON.stringify(value);
  return json === undefined ? '' : json;
}

/**
 * The inputs of a DAG tool: the inputs that the DAG declares, in its order,
 * each fixed, left to the model by `from_ai`, or open (`params` does not name
 * it, so the model decides, Spec 090); then the `params` keys that the DAG
 * does not declare. `declared` is null while the DAG is not known: only the
 * `params` of the entry show, and none is flagged.
 */
export function mergeInputs(
  params: ToolEntryModel['params'],
  declared: readonly DagInputDef[] | null
): InputRow[] {
  const named = new Map(params.map((param) => [param.name, param]));
  const rows: InputRow[] = [];
  const detail = (param: ToolEntryModel['params'][number] | undefined) => ({
    ...(param?.state === 'fixed' ? { value: valueText(param.value) } : {}),
    ...(param?.state === 'from_ai' && param.description
      ? { modelText: param.description }
      : {}),
  });
  for (const input of declared ?? []) {
    const param = named.get(input.name);
    named.delete(input.name);
    rows.push({
      name: input.name,
      type: input.type,
      required: input.required,
      description: input.description,
      fallback: input.fallback,
      state: param ? param.state : 'open',
      ...detail(param),
      undeclared: false,
    });
  }
  for (const param of named.values()) {
    rows.push({
      name: param.name,
      type: '',
      required: false,
      description: '',
      fallback: '',
      state: param.state,
      ...detail(param),
      undeclared: declared !== null,
    });
  }
  return rows;
}

/** One limit of the step, as the Limits row and the Limits block show it. */
export interface LimitFact {
  id: 'turns' | 'calls' | 'timeout' | 'tools' | 'bytes';
  label: string;
  /** The value that applies, in words. */
  value: string;
  /** True when the step sets no value and the default applies. */
  isDefault: boolean;
}

function limitText(raw: unknown): string | null {
  if (typeof raw === 'number' && Number.isFinite(raw)) return String(raw);
  return typeof raw === 'string' && raw.trim() !== '' ? raw : null;
}

/** The size of a result in bytes, as KiB when it is a whole number of them. */
function bytesText(ts: Translate, raw: unknown): string | null {
  if (raw === 0) return ts('no limit');
  if (typeof raw === 'number' && raw > 0 && raw % 1024 === 0) {
    return `${raw / 1024} KiB`;
  }
  return limitText(raw);
}

/**
 * The limits of a step that the Tools dock shows, with the default when the
 * step sets none. An agent step has the turns, the calls and its timeout. A
 * chat step has the size of the catalog and the size of a tool result.
 */
export function limitFacts(
  ts: Translate,
  step: ToolStepKind,
  value: Record<string, unknown>
): LimitFact[] {
  const withBlock =
    typeof value.with === 'object' && value.with !== null
      ? (value.with as Record<string, unknown>)
      : {};
  const fact = (
    id: LimitFact['id'],
    label: string,
    text: string | null,
    fallback: string
  ): LimitFact => ({
    id,
    label,
    value: text ?? fallback,
    isDefault: text === null,
  });
  if (step === 'chat') {
    return [
      fact(
        'tools',
        ts('Tools'),
        limitText(withBlock.max_tools),
        ts('{count} (default)', { count: DEFAULT_MAX_TOOLS })
      ),
      fact(
        'bytes',
        ts('Result size'),
        bytesText(ts, withBlock.tool_result_max_bytes),
        ts('{count} (default)', {
          count: `${DEFAULT_TOOL_RESULT_MAX_BYTES / 1024} KiB`,
        })
      ),
    ];
  }
  const timeout = value.timeout_sec;
  return [
    fact(
      'turns',
      ts('Turns'),
      limitText(withBlock.max_tool_iterations),
      ts('no limit')
    ),
    fact(
      'calls',
      ts('Calls'),
      limitText(withBlock.max_tool_calls),
      ts('{count} (default)', { count: DEFAULT_MAX_TOOL_CALLS })
    ),
    fact(
      'timeout',
      ts('Timeout'),
      typeof timeout === 'number' && timeout > 0
        ? `${timeout} s`
        : limitText(timeout),
      ts('none')
    ),
  ];
}

/** The YAML that a pick writes under `with`, for the preview of the add. */
export function pickYaml(pick: ToolPick): string {
  if (pick.category === 'builtin') {
    return stringify({
      tools: {
        builtin: { [pick.tool]: pick.tool === 'shell' ? { allow: [] } : true },
      },
    }).trimEnd();
  }
  // The DAG tools have the key `dags`; the MCP servers are in the tool map.
  return stringify(
    pick.category === 'dags'
      ? { dags: [pick.entry] }
      : { tools: { mcp: [pick.entry] } }
  ).trimEnd();
}

/** The count of issues of a row, by the strongest severity. */
export function issueSummary(issues: readonly ValidationIssue[]): {
  count: number;
  severity: 'error' | 'warning' | null;
} {
  if (issues.length === 0) return { count: 0, severity: null };
  return {
    count: issues.length,
    severity: issues.some((issue) => issue.severity === 'error')
      ? 'error'
      : 'warning',
  };
}
