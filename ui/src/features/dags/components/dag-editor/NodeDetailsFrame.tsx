// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The node details view (NDV): the frame of the YAML and the form of a step,
 * with the upstream data and the run of the step in columns of their own.
 *
 * @module features/dags/components/dag-editor
 */
import {
  ChevronLeft,
  ChevronRight,
  FileCode,
  Play,
  Wand2,
  X,
  type LucideIcon,
} from 'lucide-react';
import React from 'react';
import { NodeStatus, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { IconButton } from '@/components/ui/icon-button';
import { RepeatBadge } from '@/components/ui/repeat-badge';
import { readRepeatPolicy } from '@/components/ui/repeat-summary';
import { TabStrip, tabStripTabId } from '@/components/ui/tab-strip';
import { useRightPanelClaim } from '@/contexts/RightPanelContext';
import NodeStatusChip from '@/features/dags/components/common/NodeStatusChip';
import {
  PANEL_HEADER_CLASS,
  panelDockGeometry,
  panelMaxWidth,
  usePanelPlacement,
  useViewportWidth,
} from '@/features/dags/components/step-details/PanelShell';
import { getStoredDrawerWidth } from '@/features/dags/components/step-details/StepSideDrawer';
import { actionIcon } from '@/features/visual-editor/canvas/icons';
import type { QuickFix } from '@/features/visual-editor/doc/quickFixes';
import { buildRunOverlay } from '@/features/visual-editor/run/runOverlay';
import { stepNeighbours } from './stepOrder';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

type ApiDAGDetails = components['schemas']['DAGDetails'];
type ApiDAGRunDetails = components['schemas']['DAGRunDetails'];
type ApiStep = components['schemas']['Step'];

export type NdvState = 'docked' | 'expanded' | null;

/**
 * What the frame shows. 'file' is the YAML panel: the whole file at the
 * right of the zone, with no step and no data columns. 'step' is the node
 * view of one step: it fills the zone, with its INPUT and OUTPUT.
 */
export type NdvScope = 'file' | 'step';

/**
 * How the frame places its parts: three columns side by side, the center
 * with an INPUT | OUTPUT tablist under it (a narrow node view), or a
 * full-screen sheet of tabs.
 */
export type NdvLayout = 'columns' | 'tabs' | 'sheet';

/** Viewport widths of the layouts, in CSS pixels. */
export const NDV_WIDE_QUERY = '(min-width: 1280px)';
export const NDV_MEDIUM_QUERY = '(min-width: 768px)';

/**
 * The expanded frame shows the three columns from this frame width: the side
 * columns take about 540px and the center keeps at least 360px.
 */
export const NDV_COLUMNS_MIN_WIDTH = 1200;

/** Below this frame width the header buttons show their icon only. */
export const NDV_COMPACT_HEADER_WIDTH = 480;

/**
 * The layout for a viewport, a state and the measured width of the frame.
 * The expanded frame shows the three columns when the frame is wide enough,
 * and the tabs below that, as the docked frame does. Without a width, the
 * frame is wide enough.
 */
export function ndvLayout(
  viewport: 'wide' | 'medium' | 'narrow',
  state: NdvState,
  frameWidth?: number
): NdvLayout {
  if (viewport === 'narrow') return 'sheet';
  if (state !== 'expanded') return 'tabs';
  return frameWidth === undefined || frameWidth >= NDV_COLUMNS_MIN_WIDTH
    ? 'columns'
    : 'tabs';
}

/** The width of the element, once it is shown; undefined before. */
function useElementWidth(
  ref: React.RefObject<HTMLElement | null>,
  active: boolean
): number | undefined {
  const [width, setWidth] = React.useState<number | undefined>(undefined);
  React.useLayoutEffect(() => {
    const element = ref.current;
    if (!active || !element) return;
    const read = () => {
      const next = element.getBoundingClientRect().width;
      setWidth(next > 0 ? Math.round(next) : undefined);
    };
    read();
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver(read);
    observer.observe(element);
    return () => observer.disconnect();
  }, [ref, active]);
  return width;
}

function subscribeQuery(query: string, onChange: () => void): () => void {
  const list =
    typeof window.matchMedia === 'function' ? window.matchMedia(query) : null;
  list?.addEventListener?.('change', onChange);
  return () => list?.removeEventListener?.('change', onChange);
}

/** True when the query matches. Without matchMedia, the viewport is wide. */
function useMediaQuery(query: string): boolean {
  const subscribe = React.useCallback(
    (onChange: () => void) => subscribeQuery(query, onChange),
    [query]
  );
  const read = () =>
    typeof window.matchMedia === 'function'
      ? window.matchMedia(query).matches
      : true;
  return React.useSyncExternalStore(subscribe, read, () => true);
}

/** The step of the DAG with this server name, or one of its handlers. */
function findStep(
  dag: ApiDAGDetails | undefined,
  name: string | null
): ApiStep | undefined {
  if (!dag || name === null) return undefined;
  return (
    dag.steps?.find((step) => step.name === name) ??
    Object.values(dag.handlerOn ?? {}).find((step) => step?.name === name)
  );
}

function stepIcon(step: ApiStep | undefined): LucideIcon {
  if (!step) return FileCode;
  if (step.router) return actionIcon({ kind: 'router' });
  if (step.call) return actionIcon({ kind: 'call' });
  if (step.humanTask) {
    return actionIcon({ kind: 'action', action: 'human.task' });
  }
  if (step.approval) {
    return actionIcon({ kind: 'action', action: 'approval' });
  }
  const type = step.executorConfig?.type;
  if (!type || type === 'command' || type === 'shell') {
    return actionIcon({ kind: 'run' });
  }
  return actionIcon({ kind: 'action', action: type });
}

/** True when the name is one of a lifecycle handler and of no step. */
function isHandlerName(dag: ApiDAGDetails | undefined, name: string | null) {
  if (!dag || name === null || dag.steps?.some((s) => s.name === name)) {
    return false;
  }
  return Object.values(dag.handlerOn ?? {}).some((h) => h?.name === name);
}

export { stepNeighbours };

/** The bounds and the key of the width of the side drawer, as StepSideDrawer has them. */
const DRAWER_WIDTH_STORAGE_KEY = 'dagu.stepDetailsDrawer.width';
const MIN_DOCKED_WIDTH = 420;
const MAX_DOCKED_WIDTH = 960;
const WIDTH_STEP = 40;

/** Prefix of the DOM ids of the step tabs, for the panels they label. */
const TAB_ID_PREFIX = 'ndv';

/** A docked width inside the bounds and the viewport. */
export function clampDockedWidth(width: number, viewport: number): number {
  const max = Math.max(MIN_DOCKED_WIDTH, Math.min(MAX_DOCKED_WIDTH, viewport));
  return Math.round(Math.min(Math.max(width, MIN_DOCKED_WIDTH), max));
}

function storeDockedWidth(width: number): void {
  try {
    window.localStorage.setItem(DRAWER_WIDTH_STORAGE_KEY, String(width));
  } catch {
    // The width is only kept for the next visit.
  }
}

/** Open layers that take Escape themselves: menus, dialogs and editor widgets. */
const ESCAPE_OWNERS = [
  '[role="dialog"][data-state="open"]',
  '[role="alertdialog"][data-state="open"]',
  '[role="menu"]',
  '[role="listbox"]',
  '.suggest-widget.visible',
  '.parameter-hints-widget.visible',
  '.find-widget.visible',
].join(',');

/** True when a visible layer of the page keeps Escape for itself. */
function escapeTaken(): boolean {
  return Array.from(document.querySelectorAll(ESCAPE_OWNERS)).some(
    (layer) => !layer.closest('[inert], [hidden]')
  );
}

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'textarea:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

/** The controls in the frame that the Tab key can reach now. */
function tabStops(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (element) => !element.closest('[hidden]')
  );
}

type SideTab = 'input' | 'output';
type SheetTab = 'input' | 'center' | 'output';

export interface NodeDetailsFrameProps {
  /** Null hides the frame; its DOM node stays. Open, the scope gives the look. */
  state: NdvState;
  /** The YAML panel of the whole file, or the node view of a step. Default 'step'. */
  scope?: NdvScope;
  /** Closes the frame; the host state becomes null. */
  onClose(): void;
  /** Server name of the selected step. */
  stepName: string | null;
  /** The name for the title when the draft has renamed the step. */
  title?: string;
  /** The DAG of the edited text, for the icon and the neighbours of the step. */
  dag?: ApiDAGDetails;
  /** The run that the host shows, for the status of the step. */
  runDetails?: ApiDAGRunDetails | null;
  onSelectStep(name: string): void;
  /** Validation errors of the step: shown in a line at the foot of the frame. */
  problems?: string[];
  /** The quick fix of each problem, by index in `problems`, when it has one. */
  problemFixes?: (QuickFix | undefined)[];
  /** Called with the fixes to apply as one edit. */
  onQuickFix?(fixes: QuickFix[]): void;
  /** Controls for the text of the YAML (quick fixes, format), in the header. */
  yamlTools?: React.ReactNode;
  /** Small text after the title, such as the read-only mark of a run. */
  headerNote?: React.ReactNode;
  /**
   * The host shows the run facts of the step in the OUTPUT column, as
   * Executions does. The tabs layout then opens on OUTPUT, not on INPUT.
   */
  hasRunFacts?: boolean;
  /** Runs the step; the button is hidden without it. */
  onRunStep?(): void;
  /** The center shows the form of the step, not its YAML. */
  formMode: boolean;
  canForm: boolean;
  onFormModeChange(form: boolean): void;
  /** Where the editor puts its own controls, in the header. */
  toolbarRef(element: HTMLDivElement | null): void;
  inputRef(element: HTMLDivElement | null): void;
  outputRef(element: HTMLDivElement | null): void;
  /** The center: the YAML editor and the root of the form. */
  children: React.ReactNode;
}

/**
 * The frame of the node details view. It changes its look with CSS only, so
 * that its children, such as the YAML editor, never unmount.
 */
export function NodeDetailsFrame({
  state: hostState,
  scope = 'step',
  onClose,
  stepName: stepNameProp,
  title,
  dag,
  runDetails,
  onSelectStep,
  problems,
  problemFixes,
  onQuickFix,
  yamlTools,
  headerNote,
  hasRunFacts,
  onRunStep,
  formMode,
  canForm,
  onFormModeChange,
  toolbarRef,
  inputRef,
  outputRef,
  children,
}: NodeDetailsFrameProps) {
  const { ts } = useI18n();
  const open = hostState !== null;
  // The file is a panel at the right; a step fills the zone.
  const fileScope = scope === 'file';
  const state: NdvState = !open ? null : fileScope ? 'docked' : 'expanded';
  const stepName = fileScope ? null : stepNameProp;
  const inZone = usePanelPlacement() === 'zone';
  useRightPanelClaim('ndv', open, onClose);
  const sectionRef = React.useRef<HTMLElement>(null);
  const [width, setWidth] = React.useState(getStoredDrawerWidth);

  const viewport = useViewportWidth();
  const wide = useMediaQuery(NDV_WIDE_QUERY);
  const medium = useMediaQuery(NDV_MEDIUM_QUERY);
  // The columns need room: the frame, not the viewport, decides.
  const frameWidth = useElementWidth(sectionRef, state === 'expanded');
  const layout = ndvLayout(
    wide ? 'wide' : medium ? 'medium' : 'narrow',
    state,
    frameWidth
  );
  const shownWidth = useElementWidth(sectionRef, open);
  const compactHeader =
    shownWidth !== undefined && shownWidth < NDV_COMPACT_HEADER_WIDTH;
  const [sideTab, setSideTab] = React.useState<SideTab>(
    hasRunFacts ? 'output' : 'input'
  );
  const [sheetTab, setSheetTab] = React.useState<SheetTab>('center');

  // A draft that does not validate gives a DAG without the step: the icon
  // and the neighbours keep the last DAG that had it.
  const lastDag = React.useRef<ApiDAGDetails | undefined>(undefined);
  if (findStep(dag, stepName)) lastDag.current = dag;
  const stepDag =
    !findStep(dag, stepName) && findStep(lastDag.current, stepName)
      ? lastDag.current
      : dag;
  const step = findStep(stepDag, stepName);
  const Icon = stepIcon(step);
  const run =
    stepName === null
      ? undefined
      : buildRunOverlay(runDetails ?? undefined)[stepName];
  const repeat = React.useMemo(
    () => readRepeatPolicy(step?.repeatPolicy),
    [step?.repeatPolicy]
  );
  const { previous, next } = stepNeighbours(stepDag, stepName);
  // Without a step the frame is the YAML of the file: no data columns.
  const hasStep = stepName !== null;

  const expanded = state === 'expanded' && layout !== 'sheet';
  const docked = layout !== 'sheet' && !expanded;

  // The element that had focus when the frame opened, such as the canvas
  // node or the dock row, gets it back when the frame closes. The work is
  // in the effect body: React puts back the focus that moves during a
  // commit, which is when an effect cleanup runs.
  const openerRef = React.useRef<HTMLElement | null>(null);
  React.useLayoutEffect(() => {
    const section = sectionRef.current;
    if (open) {
      const active = document.activeElement;
      openerRef.current =
        active instanceof HTMLElement &&
        active !== document.body &&
        !section?.contains(active)
          ? active
          : null;
      return;
    }
    const opener = openerRef.current;
    openerRef.current = null;
    const focus = document.activeElement;
    const lost =
      !focus || focus === document.body || !!section?.contains(focus);
    if (opener?.isConnected && lost) opener.focus();
  }, [open]);

  // The expanded frame is modal: the focus moves into it and stays there.
  React.useLayoutEffect(() => {
    const section = sectionRef.current;
    if (!open || !expanded || !section) return;
    if (!section.contains(document.activeElement)) section.focus();
  }, [open, expanded]);

  // Escape closes the expanded frame from inside it and from the page body.
  // A handler that took the key, or an open menu, dialog or editor widget,
  // keeps it.
  const onCloseRef = React.useRef(onClose);
  onCloseRef.current = onClose;
  React.useEffect(() => {
    if (!open || !expanded) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return;
      // A pane that the host hides keeps its frame mounted: it takes no key.
      if (sectionRef.current?.closest('[inert]')) return;
      if (escapeTaken()) return;
      event.preventDefault();
      onCloseRef.current();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open, expanded]);

  const onFrameKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    const section = sectionRef.current;
    if (!expanded || event.key !== 'Tab' || !section) return;
    const stops = tabStops(section);
    const first = stops[0];
    const last = stops[stops.length - 1];
    if (!first || !last) {
      event.preventDefault();
      return;
    }
    const active = document.activeElement;
    if (event.shiftKey && (active === first || active === section)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  };

  // The docked frame takes a new width from its left edge.
  const resizeTo = (next: number, store: boolean) => {
    const clamped = clampDockedWidth(next, window.innerWidth);
    setWidth(clamped);
    if (store) storeDockedWidth(clamped);
    return clamped;
  };
  const onResizeMouseDown = (event: React.MouseEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = width;
    let latest = startWidth;
    const onMove = (move: MouseEvent) => {
      latest = resizeTo(startWidth + startX - move.clientX, false);
    };
    const onUp = () => {
      storeDockedWidth(latest);
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };
  const onResizeKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const next = {
      ArrowLeft: width + WIDTH_STEP,
      ArrowRight: width - WIDTH_STEP,
      Home: MIN_DOCKED_WIDTH,
      End: MAX_DOCKED_WIDTH,
    }[event.key];
    if (next === undefined) return;
    event.preventDefault();
    resizeTo(next, true);
  };
  const shows = {
    input:
      hasStep &&
      (layout === 'columns' ||
        (layout === 'tabs' ? sideTab === 'input' : sheetTab === 'input')),
    center: layout !== 'sheet' || sheetTab === 'center' || !hasStep,
    output:
      hasStep &&
      (layout === 'columns' ||
        (layout === 'tabs' ? sideTab === 'output' : sheetTab === 'output')),
  };

  const stepTabs = hasStep;
  const tabs: {
    id: string;
    label: string;
    selected: boolean;
    disabled?: boolean;
    controls: string;
    select(): void;
  }[] =
    layout === 'sheet'
      ? [
          {
            id: 'input',
            label: ts('Input'),
            selected: sheetTab === 'input',
            controls: 'ndv-input',
            select: () => setSheetTab('input'),
          },
          {
            id: 'parameters',
            label: ts('Parameters'),
            selected: sheetTab === 'center' && formMode,
            disabled: !canForm,
            controls: 'ndv-center',
            select: () => {
              setSheetTab('center');
              onFormModeChange(true);
            },
          },
          {
            id: 'yaml',
            label: ts('YAML'),
            selected: sheetTab === 'center' && !formMode,
            controls: 'ndv-center',
            select: () => {
              setSheetTab('center');
              onFormModeChange(false);
            },
          },
          {
            id: 'output',
            label: ts('Output'),
            selected: sheetTab === 'output',
            controls: 'ndv-output',
            select: () => setSheetTab('output'),
          },
        ]
      : [
          {
            id: 'input',
            label: ts('Input'),
            selected: sideTab === 'input',
            controls: 'ndv-input',
            select: () => setSideTab('input'),
          },
          {
            id: 'output',
            label: ts('Output'),
            selected: sideTab === 'output',
            controls: 'ndv-output',
            select: () => setSideTab('output'),
          },
        ];

  // In the sheet, the Parameters and YAML tabs both control the center.
  const centerTab =
    layout === 'sheet' && stepTabs
      ? formMode
        ? 'parameters'
        : 'yaml'
      : undefined;

  // The two side parts share their classes in each layout.
  const sideClass = {
    columns: 'shrink-0 overflow-y-auto',
    tabs: 'order-3 max-h-[40%] min-h-[10rem] overflow-y-auto',
    sheet: 'order-2 min-h-0 flex-1 overflow-y-auto',
  }[layout];

  return (
    <>
      {/* In a zone the expanded frame fills it: no backdrop beyond it. */}
      {!inZone && (
        <div
          hidden={!open || !expanded}
          aria-hidden="true"
          data-testid="ndv-backdrop"
          className="fixed inset-0 z-[60] bg-foreground/30"
          onClick={onClose}
        />
      )}
      <section
        ref={sectionRef}
        hidden={!open}
        role={expanded ? 'dialog' : undefined}
        aria-modal={expanded || undefined}
        aria-label={ts('Step details')}
        tabIndex={-1}
        onKeyDown={onFrameKeyDown}
        data-testid="ndv"
        data-state={state ?? 'closed'}
        data-layout={layout}
        data-placement={inZone ? 'zone' : 'window'}
        data-panel-shell={docked ? 'right' : undefined}
        className={cn(
          'z-[60] flex flex-col border-border bg-card shadow-lg shadow-foreground/10 outline-none',
          inZone ? 'absolute' : 'fixed',
          layout === 'sheet' && 'inset-0',
          // The whole zone when it is in one, over its row of toolbars too;
          // else the window with a small margin, over the sidebar and the bar.
          expanded && (inZone ? 'inset-0' : 'inset-3 rounded-lg border'),
          // A host can start the docked frame below its own bar.
          docked && panelDockGeometry(inZone ? 'zone' : 'window')
        )}
        style={
          docked
            ? { width: `${width}px`, maxWidth: panelMaxWidth(viewport) }
            : undefined
        }
      >
        {docked && (
          <div
            role="separator"
            aria-label={ts('Resize step details')}
            aria-orientation="vertical"
            aria-valuemin={MIN_DOCKED_WIDTH}
            aria-valuemax={MAX_DOCKED_WIDTH}
            aria-valuenow={width}
            tabIndex={0}
            title={ts('Resize step details')}
            className="group absolute left-0 top-0 z-20 h-full w-3 -translate-x-1.5 cursor-col-resize touch-none outline-none"
            onMouseDown={onResizeMouseDown}
            onKeyDown={onResizeKeyDown}
          >
            <div className="mx-auto h-full w-px bg-transparent transition-colors group-hover:bg-primary/60 group-focus:bg-primary" />
          </div>
        )}
        <header
          data-panel-header
          className={cn(PANEL_HEADER_CLASS, '@container')}
        >
          <div className="flex h-12 min-w-0 flex-1 items-center gap-2">
            {/* The title side gives way first: the buttons, Close included,
                keep their place at every width. */}
            <div className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
              <Icon
                className="h-4 w-4 shrink-0 text-muted-foreground"
                aria-hidden="true"
                data-testid="ndv-step-icon"
              />
              <h2 className="min-w-0 truncate text-base font-semibold">
                {fileScope ? ts('YAML') : (title ?? stepName ?? ts('YAML'))}
              </h2>
              {repeat && !fileScope && (
                // Read-only: the policy is edited in the YAML. It needs room,
                // so it hides when the header is narrower than 520 px.
                <span
                  data-testid="ndv-repeat"
                  className="hidden shrink-0 @[520px]:inline-flex"
                >
                  <RepeatBadge
                    policy={repeat}
                    count={run?.doneCount}
                    running={run?.status === NodeStatus.Running}
                  />
                </span>
              )}
              {headerNote}
              {run && (
                <NodeStatusChip status={run.status} size="sm">
                  {run.statusLabel}
                </NodeStatusChip>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-0.5">
              {fileScope && !formMode && yamlTools}
              {!fileScope && (
                <>
                  <IconButton
                    label={ts('Previous step')}
                    icon={ChevronLeft}
                    disabledReason={
                      previous === null
                        ? ts('This is the first step')
                        : undefined
                    }
                    onClick={() => previous !== null && onSelectStep(previous)}
                  />
                  <IconButton
                    label={ts('Next step')}
                    icon={ChevronRight}
                    disabledReason={
                      next === null ? ts('This is the last step') : undefined
                    }
                    onClick={() => next !== null && onSelectStep(next)}
                  />
                </>
              )}
              {onRunStep &&
                stepName !== null &&
                !isHandlerName(stepDag, stepName) && (
                  <Button
                    type="button"
                    variant="ghost"
                    className={cn(
                      'h-7 gap-1 text-xs',
                      compactHeader ? 'w-7 p-0' : 'px-2'
                    )}
                    title={ts('Run step')}
                    aria-label={ts('Run step')}
                    onClick={onRunStep}
                  >
                    <Play className="h-3.5 w-3.5" aria-hidden="true" />
                    {!compactHeader && ts('Run step')}
                  </Button>
                )}
              <IconButton label={ts('Close')} icon={X} onClick={onClose} />
            </div>
          </div>
        </header>
        <div
          className={cn(
            'flex min-h-0 flex-1',
            layout === 'columns' ? 'flex-row' : 'flex-col'
          )}
        >
          <div
            hidden={layout === 'columns' || !stepTabs}
            className={cn(
              'shrink-0 border-border px-2',
              layout === 'sheet' ? 'order-1 border-b' : 'order-2 border-t'
            )}
          >
            <TabStrip
              label={ts('Step data')}
              idPrefix={TAB_ID_PREFIX}
              tabs={tabs.map(({ id, label, disabled, controls }) => ({
                id,
                label,
                disabled,
                controls,
              }))}
              value={tabs.find((tab) => tab.selected)?.id ?? ''}
              onChange={(id) => tabs.find((tab) => tab.id === id)?.select()}
            />
          </div>
          <div
            ref={inputRef}
            id="ndv-input"
            role={layout === 'columns' ? 'region' : 'tabpanel'}
            aria-label={ts('Input')}
            hidden={!shows.input}
            className={cn(
              sideClass,
              layout === 'columns' && 'order-1 w-96 border-r border-border'
            )}
          />
          <div
            id="ndv-center"
            data-testid="ndv-center"
            role={centerTab ? 'tabpanel' : undefined}
            aria-labelledby={
              centerTab ? tabStripTabId(TAB_ID_PREFIX, centerTab) : undefined
            }
            hidden={!shows.center}
            className={cn(
              'flex min-h-0 min-w-0 flex-1 flex-col',
              layout === 'tabs' ? 'order-1' : 'order-2'
            )}
          >
            {/* The editor puts the YAML | Form and Step | File choices here. */}
            {/* The format and quick fix buttons of a step stand at the end of
                the same row. The row is hidden while both parts are empty. */}
            <div className="hidden min-w-0 shrink-0 items-center gap-2 border-b border-border px-3 py-1.5 has-[>div:not(:empty)]:flex">
              <div
                ref={toolbarRef}
                className="flex min-w-0 flex-1 items-center empty:hidden"
              />
              {!fileScope && !formMode && yamlTools && (
                <div className="flex shrink-0 items-center gap-0.5">
                  {yamlTools}
                </div>
              )}
            </div>
            {children}
          </div>
          <div
            ref={outputRef}
            id="ndv-output"
            role={layout === 'columns' ? 'region' : 'tabpanel'}
            aria-label={ts('Output')}
            hidden={!shows.output}
            className={cn(
              sideClass,
              layout === 'columns' && 'order-3 w-[27rem] border-l border-border'
            )}
          />
        </div>
        {!fileScope && problems && problems.length > 0 && (
          <footer
            role="alert"
            data-testid="ndv-problems"
            className="max-h-24 shrink-0 space-y-0.5 overflow-y-auto border-t border-border bg-destructive/5 px-3 py-1.5 text-xs text-destructive"
          >
            {problems.map((problem, index) => {
              const fix = problemFixes?.[index];
              return (
                <p
                  key={`${index}-${problem}`}
                  className="flex items-start gap-1.5 break-words"
                >
                  <span className="min-w-0 flex-1">{problem}</span>
                  {fix && onQuickFix && (
                    <button
                      type="button"
                      data-testid="ndv-quick-fix"
                      aria-label={ts('Quick fix: {label}', {
                        label: ts(fix.message, fix.params),
                      })}
                      title={ts(fix.message, fix.params)}
                      className="flex h-5 w-5 shrink-0 items-center justify-center rounded border border-destructive/40 hover:bg-destructive/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      onClick={() => onQuickFix([fix])}
                    >
                      <Wand2 className="h-3 w-3" aria-hidden="true" />
                    </button>
                  )}
                </p>
              );
            })}
          </footer>
        )}
      </section>
    </>
  );
}

export default NodeDetailsFrame;
