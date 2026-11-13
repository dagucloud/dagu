// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Maximize2, Pencil, Plus, Trash2 } from 'lucide-react';
import { useMemo, type ReactElement, type ReactNode } from 'react';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { MCP_PATH } from '@/features/ai-endpoints/mcpServer';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import type { DagEditOp, StepView, ValidationIssue } from '../contracts';
import { FieldIssues } from '../inspector/FieldIssues';
import {
  INPUT_STATE_CHIP,
  mergeInputs,
  pickYaml,
  rowChips,
  type DockRow,
  type InputRow,
} from '../tools/dockModel';
import { builtinToolsYaml, toolEntryYaml } from '../tools/entryYaml';
import {
  SCOPE_KINDS,
  kindLabel,
  type DockScope,
  type ToolStepKind,
} from '../tools/kinds';
import { toolNameOf } from '../tools/model';
import { OpenTextLink } from '../tools/OpenLink';
import { dagLink } from '../tools/targets';
import { useDagDetails, type DagDetails } from '../tools/useDagCatalog';
import { DagMiniature } from '../canvas/DagMiniature';
import { buildMiniGraph, type MiniBox } from '../canvas/miniGraph';
import { YamlSnippet } from '../YamlSnippet';
import { StringList } from '../inspector/sections/tools/fields';
import { makeSetField } from '../inspector/sections/chat/fields';
import { KindBadge } from '../tools/KindBadge';
import { Chip, DetailHeading, IssueBadge, KindTile, Note } from './DockParts';

/** The room of the miniature in the detail area, in px. */
const DETAIL_MINI_BOX: MiniBox = { width: 380, height: 96 };

/** What the detail area needs of the dock around it. */
export interface DetailContext {
  step: StepView;
  /** The YAML text of the step, for the YAML of an entry. */
  stepYaml: string;
  stepKind: ToolStepKind;
  stepLabel: string;
  /** True for a view that only reads, or a step with YAML errors. */
  readOnly: boolean;
  onEdit(op: DagEditOp): void;
  /** Opens the panel that edits the tool. */
  onEditTool(row: DockRow): void;
  /** Opens the enlarged view of the DAG of a DAG tool of the step. */
  onOpenDag?(row: DockRow): void;
  onRemove(row: DockRow): void;
  /** Adds the tool of a catalogue row. `stay` keeps the dock on its list. */
  onAdd(row: DockRow, stay: boolean): void;
}

function DetailHeader({
  row,
  actions,
}: {
  row: DockRow;
  actions?: ReactNode;
}): ReactElement {
  return (
    <header className="flex items-start gap-3">
      <KindTile
        kind={row.kind}
        className="size-10 [&_svg]:size-5"
        icon={undefined}
      />
      <div className="min-w-0 flex-1">
        <h2
          data-testid="ve-dock-detail-title"
          className="break-words text-sm font-semibold leading-5 text-foreground"
        >
          {row.title}
        </h2>
        <div className="mt-0.5 flex flex-wrap items-center gap-1.5">
          <KindBadge kind={row.kind} />
          {row.tag && (
            <span className="truncate font-mono text-[11px] text-muted-foreground">
              {row.tag}
            </span>
          )}
          <IssueBadge issues={row.issues} />
        </div>
      </div>
      {actions && (
        <div className="flex shrink-0 items-center gap-1">{actions}</div>
      )}
    </header>
  );
}

function YamlBlock({
  title,
  source,
  testId,
}: {
  title: ReactNode;
  source: string;
  testId: string;
}): ReactElement | null {
  if (source.trim() === '') return null;
  return (
    <section>
      <DetailHeading>{title}</DetailHeading>
      <YamlSnippet
        data-testid={testId}
        source={source}
        className="max-h-44 rounded-md bg-muted px-2.5 py-2"
      />
    </section>
  );
}

function InputLine({ input }: { input: InputRow }): ReactElement {
  const { ts } = useI18n();
  const label =
    input.state === 'fixed'
      ? ts('Fixed')
      : input.state === 'from_ai'
        ? ts('From the model')
        : ts('Open to the model');
  return (
    <li
      data-testid="ve-dock-input"
      data-state={input.state}
      className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-3 gap-y-0.5 border-t border-border py-1.5 first:border-t-0"
    >
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-1.5">
          <span className="break-all font-mono text-xs font-medium">
            {input.name}
          </span>
          {input.type && (
            <span className="text-[11px] text-muted-foreground">
              {input.type}
            </span>
          )}
          {input.required && (
            <span className="text-[11px] text-destructive">
              {ts('required')}
            </span>
          )}
          {input.undeclared && (
            <span className="text-[11px] text-warning">
              {ts('Not declared by the DAG')}
            </span>
          )}
        </div>
        {input.description && (
          <p className="text-[11px] text-muted-foreground">
            {input.description}
          </p>
        )}
        {input.state === 'from_ai' && input.modelText && (
          <p className="text-[11px] text-muted-foreground">
            {ts('The model reads: {text}', { text: input.modelText })}
          </p>
        )}
        {input.state === 'open' && input.fallback !== '' && (
          <p className="text-[11px] text-muted-foreground">
            {ts('Default: {count}', { count: input.fallback })}
          </p>
        )}
      </div>
      <div className="flex max-w-[12rem] flex-col items-end gap-0.5">
        <span
          className={cn(
            'rounded px-1.5 text-[11px] leading-4',
            INPUT_STATE_CHIP[input.state]
          )}
        >
          {label}
        </span>
        {input.state === 'fixed' && (
          <code
            title={input.value}
            className="max-w-full truncate font-mono text-[11px] text-muted-foreground"
          >
            {input.value === '' ? '""' : input.value}
          </code>
        )}
      </div>
    </li>
  );
}

/** The inputs of a DAG tool: each one fixed, from the model, or open. */
function InputsBlock({
  inputs,
  loading,
}: {
  inputs: InputRow[];
  loading: boolean;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <section>
      <DetailHeading>{ts('Inputs')}</DetailHeading>
      {inputs.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {loading ? ts('Loading...') : ts('This tool takes no input.')}
        </p>
      ) : (
        <>
          <ul
            data-testid="ve-dock-inputs"
            className="rounded-md border border-border px-2.5"
          >
            {inputs.map((input) => (
              <InputLine key={input.name} input={input} />
            ))}
          </ul>
          <p className="mt-1 text-[11px] text-muted-foreground">
            {ts(
              'The model does not see a fixed input. An input that the entry does not name stays open for the model.'
            )}
          </p>
        </>
      )}
    </section>
  );
}

/**
 * The slot of the miniature graph of the DAG tool: hidden while the DAG is not
 * loaded or has no step. The button opens the enlarged view of a tool of the
 * step; a DAG of the catalogue has the miniature only.
 */
function GraphSlot({
  dag,
  name,
  onOpen,
}: {
  dag: DagDetails['dag'];
  name: string;
  onOpen?: (() => void) | undefined;
}): ReactElement {
  const { ts } = useI18n();
  const mini = useMemo(
    () => (dag ? buildMiniGraph(dag, DETAIL_MINI_BOX) : null),
    [dag]
  );
  const shown = mini && mini.kind !== 'empty' ? mini : null;
  const steps = shown?.stats.steps ?? 0;
  const label = ts('Show the graph of {name}', { name });
  const graph = shown ? (
    <>
      <DagMiniature graph={shown} box={DETAIL_MINI_BOX} />
      <span className="pointer-events-none absolute bottom-1 right-1.5 rounded bg-card/80 px-1 text-[11px] leading-4 text-muted-foreground">
        {ts(steps === 1 ? '{count} step' : '{count} steps', { count: steps })}
      </span>
    </>
  ) : null;
  return (
    <div
      data-testid="ve-tool-graph-slot"
      role="group"
      aria-label={ts('Workflow graph')}
      className="empty:hidden"
    >
      {graph &&
        (onOpen ? (
          <button
            type="button"
            data-testid="ve-tool-graph-open"
            aria-label={label}
            title={label}
            onClick={onOpen}
            className="relative flex h-28 w-full cursor-zoom-in items-center overflow-hidden rounded-md border border-border bg-muted/40 px-2 py-1 outline-none hover:border-ring focus-visible:ring-2 focus-visible:ring-ring"
          >
            {graph}
            <Maximize2
              aria-hidden="true"
              className="pointer-events-none absolute right-1.5 top-1.5 size-3.5 text-muted-foreground"
            />
          </button>
        ) : (
          <div className="relative flex h-28 w-full items-center overflow-hidden rounded-md border border-border bg-muted/40 px-2 py-1">
            {graph}
          </div>
        ))}
    </div>
  );
}

function DagDetail({
  row,
  ctx,
}: {
  row: DockRow;
  ctx: DetailContext;
}): ReactElement {
  const { ts } = useI18n();
  const entry = row.entry;
  const name = entry?.label ?? row.title;
  const details = useDagDetails(name, true);
  const page = dagLink(name);
  const catalog = row.group === 'catalog';
  const inputs = mergeInputs(
    entry?.params ?? [],
    details.known === true ? details.inputs : null
  );
  const description = details.description || row.description;
  return (
    <>
      {entry?.kind === 'dag' && details.known === false && (
        <p role="alert" className="text-xs text-destructive">
          {ts(
            'The server does not know the DAG "{name}". The step fails when it starts.',
            { name }
          )}
        </p>
      )}
      <section>
        <DetailHeading>{ts('Description')}</DetailHeading>
        {description ? (
          <p className="whitespace-pre-line break-words text-xs text-foreground">
            {description}
          </p>
        ) : details.known === undefined ? (
          <p className="text-xs text-muted-foreground">{ts('Loading...')}</p>
        ) : (
          <p className="text-xs italic text-muted-foreground">
            {ts('This DAG has no description.')}
          </p>
        )}
        <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1">
          {page && (
            <OpenTextLink page={page}>
              <I18nText text="Open the workflow" />
            </OpenTextLink>
          )}
          <span className="text-[11px] text-muted-foreground">
            {ts('The model calls it {name}', { name: toolNameOf(name) })}
          </span>
        </div>
      </section>
      <GraphSlot
        dag={details.dag}
        name={name}
        onOpen={
          ctx.onOpenDag && !catalog && entry
            ? () => ctx.onOpenDag?.(row)
            : undefined
        }
      />
      <InputsBlock inputs={inputs} loading={details.known === undefined} />
      {!catalog && entry && (
        <YamlBlock
          title={ts('YAML of the entry')}
          testId="ve-dock-yaml"
          source={toolEntryYaml(
            ctx.stepYaml,
            entry.category,
            entry.index,
            entry.listForm === true
          )}
        />
      )}
    </>
  );
}

function McpDetail({
  row,
  ctx,
}: {
  row: DockRow;
  ctx: DetailContext;
}): ReactElement {
  const { ts } = useI18n();
  const mcp = row.entry?.mcp;
  const allow = mcp?.allow ?? [];
  const headerNames = Object.keys(mcp?.headers ?? {});
  return (
    <>
      <section>
        <DetailHeading>{ts('Server')}</DetailHeading>
        {mcp?.named ? (
          <>
            <p className="text-xs text-foreground">
              {ts('The server entry {name} of the AI page.', {
                name: row.entry?.label ?? '',
              })}
            </p>
            <p className="mt-1 text-[11px] text-muted-foreground">
              {ts(
                'The coordinator calls this server with the credentials of the entry. The step never sees them.'
              )}{' '}
              <OpenTextLink page={{ name: 'MCP', path: MCP_PATH }}>
                <I18nText text="Open the MCP servers" />
              </OpenTextLink>
            </p>
          </>
        ) : (
          <>
            <code className="block break-all rounded bg-muted px-2 py-1 font-mono text-[11px]">
              {mcp?.url || ts('No URL yet')}
            </code>
            {headerNames.length > 0 && (
              <p className="mt-1 text-[11px] text-muted-foreground">
                {ts('Headers: {names}', { names: headerNames.join(', ') })}
              </p>
            )}
            <p className="mt-1 text-[11px] text-muted-foreground">
              {ts(
                'The label of the server starts the tool names that the model sees.'
              )}
            </p>
          </>
        )}
      </section>
      <section>
        <DetailHeading>{ts('Allowed tools')}</DetailHeading>
        {allow.length === 0 ? (
          <p role="alert" className="text-xs text-warning">
            {ts(
              'The list is empty. No tool of this server joins the tools of the model.'
            )}
          </p>
        ) : (
          <ul className="flex flex-wrap gap-1" data-testid="ve-dock-allow">
            {allow.map((tool) => (
              <li
                key={tool}
                className="rounded bg-muted px-1.5 font-mono text-[11px] leading-5"
              >
                {tool}
              </li>
            ))}
          </ul>
        )}
        <p className="mt-1 text-[11px] text-muted-foreground">
          {ts(
            'Only the tools in this list join the tools of the model. The list cannot be empty.'
          )}
        </p>
      </section>
      {row.entry && (
        <YamlBlock
          title={ts('YAML of the entry')}
          testId="ve-dock-yaml"
          source={toolEntryYaml(
            ctx.stepYaml,
            row.entry.category,
            row.entry.index,
            false
          )}
        />
      )}
    </>
  );
}

function BuiltinDetail({
  row,
  ctx,
}: {
  row: DockRow;
  ctx: DetailContext;
}): ReactElement | null {
  const { ts } = useI18n();
  const tool = row.builtin;
  if (!tool) return null;
  const set = makeSetField(ctx.step.key, ctx.onEdit);
  const listOrNull = (next: string[]) => (next.length > 0 ? next : undefined);
  return (
    <>
      <section>
        <DetailHeading>{ts('Description')}</DetailHeading>
        <p className="text-xs text-foreground">{row.description}</p>
      </section>
      {tool.name === 'shell' && (
        <section className="space-y-2" data-testid="ve-dock-shell">
          <DetailHeading>{ts('Shell commands')}</DetailHeading>
          <StringList
            label={ts('Allowed commands')}
            values={tool.allow ?? []}
            readOnly={ctx.readOnly}
            onChange={(next) =>
              set(
                ['with', 'tools', 'builtin', 'shell', 'allow'],
                listOrNull(next)
              )
            }
          />
          <StringList
            label={ts('Denied commands')}
            values={tool.deny ?? []}
            readOnly={ctx.readOnly}
            onChange={(next) =>
              set(
                ['with', 'tools', 'builtin', 'shell', 'deny'],
                listOrNull(next)
              )
            }
          />
          <p className="text-[11px] text-muted-foreground">
            {ts(
              'A pattern is a command prefix. A final * means any arguments. A deny pattern wins over an allow pattern.'
            )}
          </p>
        </section>
      )}
      <YamlBlock
        title={ts('YAML of the built-in tools')}
        testId="ve-dock-yaml"
        source={builtinToolsYaml(ctx.stepYaml)}
      />
    </>
  );
}

function CatalogDetail({
  row,
  ctx,
}: {
  row: DockRow;
  ctx: DetailContext;
}): ReactElement {
  const { ts } = useI18n();
  const pick = row.pick;
  return (
    <>
      {row.kind === 'dag' && <DagDetail row={row} ctx={ctx} />}
      {row.kind === 'mcp' && (
        <section>
          <DetailHeading>{ts('Description')}</DetailHeading>
          <p className="text-xs text-foreground">
            {row.fresh
              ? ts(
                  'Connect a remote MCP server by its URL. You give the label, the URL and the allowed tools after the add.'
                )
              : ts(
                  'A server entry of the AI page. The coordinator keeps its URL and its credentials. You give the allowed tools after the add.'
                )}
          </p>
          {!row.fresh && (
            <div className="mt-1.5">
              <OpenTextLink page={{ name: 'MCP', path: MCP_PATH }}>
                <I18nText text="Open the MCP servers" />
              </OpenTextLink>
            </div>
          )}
        </section>
      )}
      {row.kind === 'builtin' && (
        <section>
          <DetailHeading>{ts('Description')}</DetailHeading>
          <p className="text-xs text-foreground">{row.description}</p>
          {row.pick?.category === 'builtin' && row.pick.tool === 'shell' && (
            <p className="mt-1 text-[11px] text-muted-foreground">
              {ts(
                'The shell tool needs a command pattern. You add it after the add.'
              )}
            </p>
          )}
        </section>
      )}
      {pick && (
        <YamlBlock
          title={ts('What the add writes')}
          testId="ve-dock-add-yaml"
          source={pickYaml(pick)}
        />
      )}
    </>
  );
}

/**
 * The detail area of the Tools dock for one row: the description, the inputs
 * and the YAML of a DAG tool; the server and the allowed tools of an MCP
 * server; the switch and the shell rules of a built-in tool; for a catalogue
 * row, what the add writes. The issues of the entry come first. A step row
 * has Edit and Remove; a catalogue row has Add.
 */
export function RowDetail({
  row,
  ctx,
}: {
  row: DockRow;
  ctx: DetailContext;
}): ReactElement {
  const { ts } = useI18n();
  const catalog = row.group === 'catalog';
  const chips = catalog ? [] : rowChips(ts, row);
  const issues: ValidationIssue[] = row.issues;
  const actions = catalog ? (
    <Button
      type="button"
      size="sm"
      data-testid="ve-dock-add"
      disabled={ctx.readOnly}
      className="h-7 gap-1 px-2 text-xs"
      onClick={() => ctx.onAdd(row, false)}
    >
      <Plus className="size-3.5" aria-hidden="true" />
      {ts('Add to {name}', { name: ctx.stepLabel })}
    </Button>
  ) : (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        data-testid="ve-dock-edit"
        className="h-7 gap-1 px-2 text-xs"
        onClick={() => ctx.onEditTool(row)}
      >
        <Pencil className="size-3.5" aria-hidden="true" />
        {ts('Edit')}
      </Button>
      {!ctx.readOnly && (
        <IconButton
          tone="destructive"
          icon={Trash2}
          label={ts('Remove tool {name}', { name: row.title })}
          data-testid="ve-dock-remove"
          onClick={() => ctx.onRemove(row)}
        />
      )}
    </>
  );
  return (
    <div
      data-testid="ve-dock-detail-body"
      data-kind={row.kind}
      className="space-y-3 p-4"
    >
      <DetailHeader row={row} actions={actions} />
      {chips.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {chips.map((chip) => (
            <Chip key={chip.id} chip={chip} />
          ))}
        </div>
      )}
      {issues.length > 0 && (
        <section data-testid="ve-dock-detail-issues">
          <DetailHeading>{ts('Issues')}</DetailHeading>
          <FieldIssues issues={issues} className="mt-0" />
        </section>
      )}
      {row.entry?.kind === 'invalid' && (
        <p className="text-xs text-destructive">{row.description}</p>
      )}
      {catalog ? (
        <CatalogDetail row={row} ctx={ctx} />
      ) : row.builtin ? (
        <BuiltinDetail row={row} ctx={ctx} />
      ) : row.entry?.kind === 'dag' ? (
        <DagDetail row={row} ctx={ctx} />
      ) : row.entry?.kind === 'mcp' ? (
        <McpDetail row={row} ctx={ctx} />
      ) : null}
      <p className="sr-only">{kindLabel(ts, row.kind)}</p>
    </div>
  );
}

/** The detail area when the list shows no row: what the kinds of tool of the dock are. */
export function EmptyDetail({
  stepKind,
  scope,
}: {
  stepKind: ToolStepKind;
  scope: DockScope;
}): ReactElement {
  const { ts } = useI18n();
  const all: { kind: 'dag' | 'mcp' | 'builtin'; text: string }[] = [
    {
      kind: 'dag',
      text: ts('Runs another workflow. The model gives it the inputs.'),
    },
    {
      kind: 'mcp',
      text: ts(
        'Gives the tools of a remote MCP server. Use a server of the AI page, or a URL.'
      ),
    },
    ...(stepKind === 'agent'
      ? [
          {
            kind: 'builtin' as const,
            text: ts('Gives a tool of the CLI of the agent.'),
          },
        ]
      : []),
  ];
  // Each dock explains the kinds that it lists.
  const kinds = all.filter((item) => SCOPE_KINDS[scope].includes(item.kind));
  return (
    <div data-testid="ve-dock-detail-empty" className="space-y-2 p-4">
      <h2 className="text-sm font-semibold">
        {scope === 'dags' ? ts('Choose a DAG') : ts('Choose a tool')}
      </h2>
      <p className="text-xs text-muted-foreground">
        {scope === 'dags'
          ? ts('Select a DAG in the list to see its details here.')
          : ts('Select a tool in the list to see its details here.')}
      </p>
      <ul className="space-y-2 pt-1">
        {kinds.map((item) => (
          <li key={item.kind} className="flex items-start gap-2.5">
            <KindTile kind={item.kind} className="size-7 [&_svg]:size-3.5" />
            <div className="min-w-0">
              <div className="flex items-center gap-1.5 text-xs font-medium">
                {kindLabel(ts, item.kind)}
              </div>
              <p className="text-[11px] text-muted-foreground">{item.text}</p>
            </div>
          </li>
        ))}
      </ul>
      <Note>
        {ts('A tool is something that the model can call during the step.')}
      </Note>
    </div>
  );
}
