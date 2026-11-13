// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ChevronDown,
  ChevronUp,
  Gauge,
  Pencil,
  Plus,
  Trash2,
} from 'lucide-react';
import {
  Fragment,
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
} from 'react';
import { MCP_PATH } from '@/features/ai-endpoints/mcpServer';
import type { DagIdentity } from '@/features/ai-endpoints/useEndpointChoices';
import { useMcpServerChoices } from '@/features/ai-endpoints/useMcpServerChoices';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import type {
  DagDocModel,
  DagEditOp,
  StepView,
  ToolSelection,
  ValidationIssue,
} from '../contracts';
import { stepYamlFromModel } from '../inspector/stepYamlText';
import { builtinIcon } from '../tools/builtin';
import {
  buildToolSections,
  flatRows,
  limitFacts,
  matchesWords,
  queryWords,
  rowChips,
  type DockRow,
  type DockSection,
  type EmptyReason,
} from '../tools/dockModel';
import {
  SCOPE_KINDS,
  kindAvailability,
  kindClasses,
  kindIcon,
  kindLabel,
  kindOfCategory,
  scopeOfKind,
  type DockScope,
  type ToolKind,
  type ToolStepKind,
} from '../tools/kinds';
import { KindBadge } from '../tools/KindBadge';
import type { ToolPick } from '../tools/pick';
import { OpenTextLink } from '../tools/OpenLink';
import { readBuiltinTools, readStepTools } from '../tools/parse';
import { useDagCatalog } from '../tools/useDagCatalog';
import {
  Chip,
  DockSearch,
  GroupLabel,
  IssueBadge,
  KindTile,
  Note,
  SectionHead,
} from './DockParts';
import { LimitFigures, LimitsDetail } from './LimitsDetail';
import { EmptyDetail, RowDetail, type DetailContext } from './ToolDetail';

export interface ToolsDockProps {
  model: DagDocModel;
  /** The chat step or the agent step that holds the tools. */
  step: StepView;
  /** The name of the step, for the add button of the detail. */
  stepLabel: string;
  stepKind: ToolStepKind;
  /**
   * What the dock lists and edits: the DAG tools (`dags`, the dock of the DAGs
   * card), or the MCP servers and the built-in tools (`tools`, the dock of the
   * Tools card). Each dock keeps to its kinds: the other kinds do not show.
   */
  scope: DockScope;
  /** The issues of the step, by field. */
  fieldIssues: Record<string, ValidationIssue[]>;
  /** True for a view that only reads, or a step with YAML errors. */
  locked: boolean;
  /** The DAG that holds the step: it is no tool of itself, and the MCP server entries that it may use depend on it. */
  dag?: DagIdentity | undefined;
  /** The dock opened from an Add button: the catalogue shows, and the tools of the step fold. */
  adding: boolean;
  onEdit(op: DagEditOp): void;
  /** Selects a tool entry: its panel replaces this one. */
  onOpenTool(tool: ToolSelection): void;
  /** Opens the enlarged view of the DAG of a DAG tool: it replaces this panel. */
  onOpenDag?(tool: ToolSelection): void;
  /** Adds a tool, and its panel replaces this one. */
  onPick(pick: ToolPick): void;
  onClose(): void;
}

const LIMITS_KEY = 'limits';

type Item =
  | { type: 'section'; key: string; section: DockSection; open: boolean }
  | { type: 'row'; key: string; row: DockRow; section: string | null }
  | { type: 'limits'; key: typeof LIMITS_KEY };

function isTextTarget(target: EventTarget): boolean {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable ||
      ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName) ||
      !!target.closest('.monaco-editor'))
  );
}

/** The selection that opens the panel of a row of the step. */
function selectionOf(step: StepView, row: DockRow): ToolSelection | null {
  if (row.builtin) return { step: step.key, category: 'builtin', index: 0 };
  if (row.entry && row.group === 'step') {
    return {
      step: step.key,
      category: row.entry.category,
      index: row.entry.index,
    };
  }
  return null;
}

function EmptyNote({
  reason,
  kind,
}: {
  reason: EmptyReason;
  kind: ToolKind;
}): ReactElement {
  const { ts } = useI18n();
  const text: Record<EmptyReason, string> = {
    loading: ts('Loading DAGs...'),
    'no-dags': ts(
      'No other DAG found. Create a DAG in the DAGs directory to use it as a tool.'
    ),
    'all-dags': ts('Every other DAG is a tool of this step already.'),
    'no-servers': ts(
      'No MCP server entry for this DAG. Add one on the AI page, or use a URL.'
    ),
    'all-servers': ts('Every MCP server entry is in this step already.'),
    'all-builtin': ts('All built-in tools are enabled.'),
  };
  return (
    <div
      data-testid="ve-dock-empty"
      data-reason={reason}
      data-kind={kind}
      className="mx-3 my-1 rounded-md border border-dashed border-border px-3 py-2 text-xs text-muted-foreground"
    >
      <p>{text[reason]}</p>
      {reason === 'no-servers' && (
        <p className="mt-1">
          <OpenTextLink page={{ name: 'MCP', path: MCP_PATH }}>
            <I18nText text="Open the MCP servers" />
          </OpenTextLink>
        </p>
      )}
    </div>
  );
}

function ToolRow({
  id,
  row,
  active,
  selected,
  locked,
  onSelect,
  onEdit,
  onRemove,
  onAdd,
  showKind,
}: {
  id: string;
  row: DockRow;
  active: boolean;
  selected: boolean;
  locked: boolean;
  onSelect(): void;
  onEdit(): void;
  onRemove(): void;
  onAdd(stay: boolean): void;
  /** False in a dock of one kind: every row is of that kind. */
  showKind: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const chips = row.group === 'step' ? rowChips(ts, row) : [];
  const catalog = row.group === 'catalog';
  const icon = row.builtin
    ? builtinIcon(row.builtin.name)
    : row.pick?.category === 'builtin'
      ? builtinIcon(row.pick.tool)
      : undefined;
  const stop = (event: { stopPropagation(): void }) => event.stopPropagation();
  return (
    <div
      id={id}
      role="treeitem"
      aria-level={2}
      aria-selected={selected}
      data-testid="ve-dock-row"
      data-row={row.id}
      data-kind={row.kind}
      data-group={row.group}
      onClick={(event) => {
        onSelect();
        if (event.detail > 1 && !catalog) onEdit();
      }}
      className={cn(
        'group mx-2 flex cursor-default items-start gap-3 rounded-md px-2 py-2',
        selected ? 'bg-accent text-accent-foreground' : 'hover:bg-muted/60',
        active && 'ring-1 ring-inset ring-ring/40'
      )}
    >
      <KindTile kind={row.kind} icon={icon} className="mt-0.5" />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-baseline gap-2">
          <span className="truncate text-sm font-medium">{row.title}</span>
          {row.tag && (
            <span className="shrink-0 max-w-[8rem] truncate font-mono text-[10px] text-muted-foreground">
              {row.tag}
            </span>
          )}
        </span>
        <span
          className={cn(
            'block truncate text-xs text-muted-foreground',
            row.blank && 'italic'
          )}
          title={row.description}
        >
          {row.blank ? ts('No description') : row.description}
        </span>
        {chips.length > 0 && (
          <span className="mt-1 flex min-w-0 flex-wrap gap-1">
            {chips.map((chip) => (
              <Chip key={chip.id} chip={chip} />
            ))}
          </span>
        )}
      </span>
      <span className="flex shrink-0 flex-col items-end gap-1">
        <span className="flex items-center gap-1.5">
          <IssueBadge issues={row.issues} />
          {showKind && <KindBadge kind={row.kind} />}
        </span>
        {/* Not buttons: an item of the tree holds no control. The keyboard
            has Enter, Shift+Enter and Delete, and the detail has real buttons. */}
        <span
          aria-hidden="true"
          className={cn(
            'flex h-6 items-center gap-0.5',
            !selected && 'opacity-0 group-hover:opacity-100'
          )}
        >
          {catalog && !locked && (
            <span
              role="presentation"
              title={ts('Add')}
              data-testid="ve-dock-row-add"
              onClick={(event) => {
                stop(event);
                onAdd(event.shiftKey);
              }}
              className="flex h-6 cursor-pointer items-center gap-0.5 rounded px-1.5 text-[11px] font-medium text-foreground hover:bg-background"
            >
              <Plus className="size-3" aria-hidden="true" />
              {ts('Add')}
            </span>
          )}
          {!catalog && !row.builtin && (
            <span
              role="presentation"
              title={ts('Edit')}
              data-testid="ve-dock-row-edit"
              onClick={(event) => {
                stop(event);
                onEdit();
              }}
              className="flex size-6 cursor-pointer items-center justify-center rounded hover:bg-background"
            >
              <Pencil className="size-3.5" aria-hidden="true" />
            </span>
          )}
          {!catalog && !locked && (
            <span
              role="presentation"
              title={ts('Remove tool {name}', { name: row.title })}
              data-testid="ve-dock-row-remove"
              onClick={(event) => {
                stop(event);
                onRemove();
              }}
              className="flex size-6 cursor-pointer items-center justify-center rounded hover:bg-background hover:text-destructive"
            >
              <Trash2 className="size-3.5" aria-hidden="true" />
            </span>
          )}
        </span>
      </span>
    </div>
  );
}

/**
 * The body of the Tools dock of a chat step or an agent step. A search box
 * filters every tool at once: the tools of the step (DAG tools, MCP servers,
 * built-in tools) and the catalogue of what the step can add (the DAGs of the
 * DAGs directory, the MCP server entries of the AI page, a new server with a
 * URL, the built-in tools that are off). The list has sections with a count
 * that fold, and one row for each tool with its icon, name, description, kind
 * and facts. The detail area shows the selected tool in full, or the limits
 * of the step: it sits at the right of the list in a wide dock and under it in
 * a narrow one. The list is a tree that the search box drives (up, down, home,
 * end, left and right; Enter edits a tool, adds a catalogue tool or folds a
 * section; Shift+Enter adds and keeps the list; Delete removes). Every edit
 * is the operation that the step form writes.
 */
export function ToolsDock({
  model,
  step,
  stepLabel,
  stepKind,
  scope,
  fieldIssues,
  locked,
  dag,
  adding,
  onEdit,
  onOpenTool,
  onOpenDag,
  onPick,
  onClose,
}: ToolsDockProps): ReactElement {
  const { ts } = useI18n();
  const dags = scope === 'dags';
  // A dock of one kind has no heading for a kind and no kind badge on a row.
  const oneKind = SCOPE_KINDS[scope].length === 1;
  const detailsLabel = dags
    ? ts('Details of the DAG')
    : ts('Details of the tool');
  const baseId = useId();
  const listId = `${baseId}-list`;
  const searchRef = useRef<HTMLInputElement>(null);
  const detailRef = useRef<HTMLElement>(null);
  const [query, setQuery] = useState('');
  // A narrow dock shows the detail under the list: it can fold to its title.
  const [detailOpen, setDetailOpen] = useState(true);
  const words = queryWords(query);
  const searching = words.length > 0;
  const tools = useMemo(() => readStepTools(step.value), [step.value]);
  const builtin = useMemo(
    () => (stepKind === 'agent' ? readBuiltinTools(step.value) : []),
    [step.value, stepKind]
  );
  const catalog = useDagCatalog(true, query);
  const mcp = useMcpServerChoices(dag);
  const servers = useMemo(
    () => mcp.choices.map((choice) => choice.name),
    [mcp.choices]
  );
  const stepYaml = useMemo(() => stepYamlFromModel(model, step), [model, step]);

  const sections = useMemo(
    () =>
      buildToolSections({
        ts,
        step: stepKind,
        scope,
        query,
        entries: tools.entries,
        builtin,
        dags: catalog.dags,
        dagsLoading: catalog.loading,
        dagsComplete: catalog.complete,
        servers,
        exclude: dag?.name,
        fieldIssues,
        canAdd: !locked,
      }),
    [
      ts,
      stepKind,
      scope,
      query,
      tools.entries,
      builtin,
      catalog.dags,
      catalog.loading,
      catalog.complete,
      servers,
      dag?.name,
      fieldIssues,
      locked,
    ]
  );

  // The kinds of this dock only: a tool of another kind is in the other dock.
  const scoped = useMemo(
    () =>
      tools.entries.filter(
        (entry) => scopeOfKind(kindOfCategory(entry.category)) === scope
      ),
    [tools.entries, scope]
  );
  const hasTools = scoped.length + (scope === 'tools' ? builtin.length : 0) > 0;
  // The tools of the step fold when the dock opens to add one, and the
  // catalogue folds when the dock opens on the tools. A step with no tool has
  // nothing to fold.
  const [folded, setFolded] = useState<ReadonlySet<string>>(() => {
    const kinds = SCOPE_KINDS[scope];
    return adding
      ? new Set(kinds.map((kind) => `step:${kind}`))
      : hasTools
        ? new Set(kinds.map((kind) => `catalog:${kind}`))
        : new Set();
  });
  const toggle = (id: string) =>
    setFolded((current) => {
      const next = new Set(current);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const limitTexts = useMemo(
    () => limitFacts(ts, stepKind, step.value),
    [ts, stepKind, step.value]
  );
  // The limits sit on the Tools card, and on the DAGs card of a step that has
  // no Tools card: the dock of that card shows them. A step with neither card
  // has them in the Tools dock.
  const hasToolsCard =
    tools.entries.some((entry) => entry.category === 'mcp') ||
    builtin.length > 0;
  const hasDagsCard = tools.entries.some((entry) => entry.category === 'dags');
  const limitsHere =
    scope === 'tools'
      ? hasToolsCard || !hasDagsCard
      : !hasToolsCard && hasDagsCard;
  const showLimits =
    limitsHere &&
    (!searching ||
      matchesWords(words, [
        ts('Limits'),
        ...limitTexts.map((fact) => fact.label),
        'limit',
      ]));

  // The items in the order of the screen: the keyboard moves on them.
  const items = useMemo(() => {
    const list: Item[] = [];
    // The limits come first: they hold for the whole step.
    if (showLimits) list.push({ type: 'limits', key: LIMITS_KEY });
    for (const section of sections) {
      const open = oneKind || searching || !folded.has(section.id);
      if (!oneKind) {
        list.push({ type: 'section', key: section.id, section, open });
      }
      if (open) {
        for (const row of section.rows) {
          list.push({ type: 'row', key: row.id, row, section: section.id });
        }
      }
    }
    return list;
  }, [sections, folded, searching, showLimits, oneKind]);

  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  // A dock that opens to add starts on the first tool to add.
  const firstRow =
    (adding
      ? items.find(
          (item) => item.type === 'row' && item.row.group === 'catalog'
        )
      : undefined) ?? items.find((item) => item.type === 'row');
  const active =
    items.find((item) => item.key === activeKey) ?? firstRow ?? items[0];
  const selectedItem =
    items.find((item) => item.key === selectedKey && item.type !== 'section') ??
    (active && active.type !== 'section' ? active : firstRow);
  const selectedRow =
    selectedItem?.type === 'row' ? selectedItem.row : undefined;
  const domId = (key: string | undefined) =>
    key === undefined
      ? undefined
      : `${baseId}-${items.findIndex((i) => i.key === key)}`;

  const activate = useCallback((item: Item | undefined) => {
    if (!item) return;
    setActiveKey(item.key);
    if (item.type !== 'section') setSelectedKey(item.key);
  }, []);
  useEffect(() => {
    const id = domId(active?.key);
    if (id) document.getElementById(id)?.scrollIntoView?.({ block: 'nearest' });
    // The element moves when the active item changes.
  }, [active?.key, items.length]);

  const changeQuery = (value: string) => {
    setQuery(value);
    setActiveKey(null);
    setSelectedKey(null);
  };

  // Operations: the same as the step form and the add of the editor.
  const removeRow = (row: DockRow) => {
    if (locked) return;
    if (row.builtin) {
      onEdit({
        kind: 'setStepField',
        step: step.key,
        path: ['with', 'tools', 'builtin', row.builtin.name],
        value: undefined,
      });
    } else if (row.entry) {
      onEdit({
        kind: 'editStepTools',
        step: step.key,
        change: {
          kind: 'remove',
          category: row.entry.category,
          index: row.entry.index,
        },
      });
    }
  };
  const editRow = (row: DockRow) => {
    const selection = selectionOf(step, row);
    if (selection) onOpenTool(selection);
  };
  const openDagRow = (row: DockRow) => {
    const selection = selectionOf(step, row);
    if (selection) onOpenDag?.(selection);
  };
  // The new tool is the last of its list; the list selects it when it shows.
  const [pending, setPending] = useState<string | null>(null);
  const addRow = (row: DockRow, stay: boolean) => {
    const pick = row.pick;
    if (!pick || locked) return;
    if (!stay) {
      onPick(pick);
      return;
    }
    if (pick.category === 'builtin') {
      onEdit({
        kind: 'setStepField',
        step: step.key,
        path: ['with', 'tools', 'builtin', pick.tool],
        value: pick.tool === 'shell' ? { allow: [] } : true,
      });
      setPending(`step:builtin:${pick.tool}`);
    } else {
      const { category } = pick;
      onEdit({
        kind: 'editStepTools',
        step: step.key,
        change: { kind: 'add', category, entry: pick.entry },
      });
      setPending(
        `step:${category}[${tools.entries.filter((e) => e.category === category).length}]`
      );
    }
  };
  useEffect(() => {
    if (pending === null) return;
    const item = items.find((entry) => entry.key === pending);
    if (!item) return;
    activate(item);
    setPending(null);
  }, [pending, items, activate]);

  const move = (to: number) => {
    const last = items.length - 1;
    activate(items[Math.max(0, Math.min(to, last))]);
  };
  const index = active ? items.findIndex((i) => i.key === active.key) : -1;

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    // Enter and Escape of an input method confirm or cancel its text.
    if (event.nativeEvent.isComposing) return;
    const empty = query === '';
    switch (event.key) {
      case 'ArrowDown':
        move(index + 1);
        break;
      case 'ArrowUp':
        move(index - 1);
        break;
      // Shift+Home and Shift+End keep selecting the text of the search.
      case 'Home':
        if (event.shiftKey) return;
        move(0);
        break;
      case 'End':
        if (event.shiftKey) return;
        move(items.length - 1);
        break;
      case 'ArrowRight':
        if (!empty || active?.type !== 'section') return;
        if (!active.open) toggle(active.section.id);
        break;
      case 'ArrowLeft': {
        if (!empty || !active) return;
        if (active.type === 'section') {
          if (active.open) toggle(active.section.id);
        } else if (active.type === 'row' && active.section) {
          activate(items.find((item) => item.key === active.section));
        } else return;
        break;
      }
      case 'Enter':
        if (!active) break;
        if (active.type === 'section') toggle(active.section.id);
        else if (active.type === 'limits') {
          detailRef.current?.querySelector<HTMLElement>('input')?.focus();
        } else if (active.row.group === 'catalog') {
          addRow(active.row, event.shiftKey);
        } else editRow(active.row);
        break;
      case 'Delete':
        if (!empty || active?.type !== 'row' || active.row.group !== 'step') {
          return;
        }
        removeRow(active.row);
        break;
      case 'Escape':
        if (!empty) changeQuery('');
        else onClose();
        break;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
  };

  // "/" gives the focus to the search from anywhere in the dock but a field.
  const onDockKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === '/' && !isTextTarget(event.target)) {
      event.preventDefault();
      searchRef.current?.focus();
    } else if (
      event.key === 'Escape' &&
      isTextTarget(event.target) &&
      event.target !== searchRef.current
    ) {
      // Escape in a field of the detail goes back to the search.
      event.preventDefault();
      event.stopPropagation();
      searchRef.current?.focus();
    }
  };

  const ctx: DetailContext = {
    step,
    stepYaml,
    stepKind,
    stepLabel,
    readOnly: locked,
    onEdit,
    onEditTool: editRow,
    ...(onOpenDag ? { onOpenDag: openDagRow } : {}),
    onRemove: removeRow,
    onAdd: addRow,
  };

  const stepSections = sections.filter((s) => s.group === 'step');
  const catalogSections = sections.filter((s) => s.group === 'catalog');
  const rowCount = (list: DockSection[]) =>
    list.reduce((total, s) => total + s.rows.length, 0);
  const noRows = flatRows(sections).length === 0;
  const builtinNote =
    scope === 'tools' && stepKind === 'chat' && !locked && !searching
      ? kindAvailability(ts, 'chat', new Set()).builtin.note
      : null;

  const renderSection = (section: DockSection): ReactElement => {
    const item = items.find((entry) => entry.key === section.id);
    const open = oneKind || (item?.type === 'section' ? item.open : false);
    const Icon = kindIcon(section.kind);
    return (
      <div
        key={section.id}
        id={`${baseId}-${items.findIndex((i) => i.key === section.id)}`}
        role="treeitem"
        aria-level={1}
        aria-expanded={open}
        aria-selected={false}
        aria-label={`${section.title} ${section.rows.length}`}
        data-testid="ve-dock-group"
        data-section={section.id}
      >
        {!oneKind && (
          <SectionHead
            title={section.title}
            count={section.rows.length}
            open={open}
            active={active?.key === section.id}
            icon={Icon}
            iconClass={kindClasses(section.kind).text}
            onToggle={() => {
              activate(item);
              toggle(section.id);
            }}
          />
        )}
        {open && (
          <div role="group">
            {section.rows.map((row) => {
              const at = items.findIndex((entry) => entry.key === row.id);
              return (
                <ToolRow
                  key={row.id}
                  id={`${baseId}-${at}`}
                  row={row}
                  active={active?.key === row.id}
                  selected={selectedItem?.key === row.id}
                  locked={locked}
                  onSelect={() =>
                    activate(items.find((entry) => entry.key === row.id))
                  }
                  onEdit={() => editRow(row)}
                  onRemove={() => removeRow(row)}
                  onAdd={(stay) => addRow(row, stay)}
                  showKind={!oneKind}
                />
              );
            })}
            {section.empty && !searching && (
              <EmptyNote reason={section.empty} kind={section.kind} />
            )}
          </div>
        )}
      </div>
    );
  };

  return (
    <div
      data-testid="ve-tools-dock"
      data-scope={scope}
      data-mode={adding ? 'add' : 'list'}
      onKeyDown={onDockKeyDown}
      className="@container flex min-h-0 flex-1 flex-col"
    >
      <div className="border-b border-border p-3">
        <DockSearch
          value={query}
          label={dags ? ts('Search DAGs') : ts('Search tools')}
          placeholder={
            dags ? ts('Search DAGs') : ts('Search tools and servers')
          }
          listId={listId}
          activeId={domId(active?.key)}
          inputRef={searchRef}
          keyShortcuts="/"
          onChange={changeQuery}
          onKeyDown={onKeyDown}
        />
      </div>
      <div className="flex min-h-0 flex-1 flex-col @2xl:flex-row">
        <div className="flex min-h-0 flex-1 flex-col @2xl:w-1/2 @2xl:flex-none @2xl:border-r @2xl:border-border">
          <div
            id={listId}
            role="tree"
            aria-label={dags ? ts('DAGs') : ts('Tools')}
            onMouseDown={(event) => event.preventDefault()}
            className="min-h-0 flex-1 overflow-y-auto pb-2"
          >
            {noRows && searching && (
              <div
                data-testid="ve-dock-no-match"
                className="space-y-2 px-4 py-6 text-center text-sm text-muted-foreground"
              >
                <p>
                  {dags
                    ? ts('No DAG matches the search.')
                    : ts('No tool matches the search.')}
                </p>
                <button
                  type="button"
                  onClick={() => changeQuery('')}
                  className="rounded-md border px-2 py-1 text-xs font-medium text-foreground hover:bg-accent"
                >
                  {dags ? ts('Show all DAGs') : ts('Show all tools')}
                </button>
              </div>
            )}
            {showLimits && (
              <Fragment>
                <div
                  id={`${baseId}-${items.findIndex((i) => i.key === LIMITS_KEY)}`}
                  role="treeitem"
                  aria-level={1}
                  aria-selected={selectedItem?.key === LIMITS_KEY}
                  data-testid="ve-dock-limits-row"
                  onClick={() =>
                    activate(items.find((i) => i.key === LIMITS_KEY))
                  }
                  className={cn(
                    'mx-2 flex cursor-default items-start gap-3 rounded-md px-2 py-2',
                    selectedItem?.key === LIMITS_KEY
                      ? 'bg-accent text-accent-foreground'
                      : 'hover:bg-muted/60',
                    active?.key === LIMITS_KEY &&
                      'ring-1 ring-inset ring-ring/40'
                  )}
                >
                  <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground">
                    <Gauge className="size-4" aria-hidden="true" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">
                      {ts('Limits')}
                    </span>
                    <LimitFigures facts={limitTexts} />
                  </span>
                </div>
                <div
                  role="presentation"
                  className="mb-1 mt-1 border-b border-border"
                />
              </Fragment>
            )}
            {(stepSections.length > 0 || (!searching && !hasTools)) && (
              <Fragment>
                <GroupLabel
                  title={ts('In this step')}
                  count={rowCount(stepSections)}
                />
                {stepSections.map(renderSection)}
                {!hasTools && !searching && (
                  <div className="px-3 py-2">
                    <Note data-testid="ve-dock-no-tools">
                      {locked
                        ? dags
                          ? ts('No DAGs')
                          : ts('No tools')
                        : dags
                          ? ts(
                              'No DAGs yet. The model has nothing to call. Pick a DAG below.'
                            )
                          : ts(
                              'No tools yet. The model has nothing to call. Pick a tool below.'
                            )}
                    </Note>
                  </div>
                )}
              </Fragment>
            )}
            {catalogSections.length > 0 && (
              <Fragment>
                <GroupLabel
                  title={ts('Available to add')}
                  count={rowCount(catalogSections)}
                />
                {catalogSections.map(renderSection)}
                {builtinNote && (
                  <p
                    data-testid="ve-dock-builtin-note"
                    className="px-4 py-2 text-[11px] text-muted-foreground"
                  >
                    {kindLabel(ts, 'builtin')}: {builtinNote}
                  </p>
                )}
              </Fragment>
            )}
          </div>
        </div>
        <button
          type="button"
          data-testid="ve-dock-detail-toggle"
          aria-expanded={detailOpen}
          aria-controls={`${baseId}-detail`}
          onClick={() => setDetailOpen((value) => !value)}
          className="flex h-8 shrink-0 items-center gap-1.5 border-t border-border bg-muted/40 px-3 text-left text-xs font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring @2xl:hidden"
        >
          {detailOpen ? (
            <ChevronDown className="size-3.5 shrink-0" aria-hidden="true" />
          ) : (
            <ChevronUp className="size-3.5 shrink-0" aria-hidden="true" />
          )}
          <span className="min-w-0 flex-1 truncate">
            {selectedItem?.type === 'limits'
              ? ts('Limits')
              : (selectedRow?.title ?? detailsLabel)}
          </span>
        </button>
        <section
          id={`${baseId}-detail`}
          ref={detailRef}
          aria-label={detailsLabel}
          data-testid="ve-dock-detail"
          className={cn(
            'max-h-[40%] min-h-0 shrink-0 overflow-y-auto @2xl:block @2xl:max-h-none @2xl:min-w-0 @2xl:flex-1 @2xl:border-t-0',
            !detailOpen && 'hidden'
          )}
        >
          {selectedItem?.type === 'limits' ? (
            <LimitsDetail
              step={step}
              stepKind={stepKind}
              readOnly={locked}
              fieldIssues={fieldIssues}
              onEdit={onEdit}
            />
          ) : selectedRow ? (
            <RowDetail key={selectedRow.id} row={selectedRow} ctx={ctx} />
          ) : (
            <EmptyDetail stepKind={stepKind} scope={scope} />
          )}
        </section>
      </div>
      <footer className="border-t border-border px-4 py-2 text-[11px] text-muted-foreground">
        <p
          className="truncate"
          title={
            locked
              ? ts('Up and Down to move, Enter to edit, / to search')
              : ts(
                  'Up and Down to move, Enter to edit or add, Shift+Enter to add and keep the list, Delete to remove, / to search'
                )
          }
        >
          {noRows && searching
            ? ts('Nothing matches: Esc clears the search')
            : locked
              ? ts('Up and Down to move, Enter to edit, / to search')
              : ts(
                  'Up and Down to move, Enter to edit or add, Shift+Enter to add and keep the list, Delete to remove, / to search'
                )}
        </p>
      </footer>
    </div>
  );
}
