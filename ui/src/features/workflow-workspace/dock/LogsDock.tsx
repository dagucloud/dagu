// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronUp, Maximize2, Minimize2, X } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
  type PointerEvent,
} from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { useActiveRightPanel } from '@/contexts/RightPanelContext';
import { useI18n } from '@/i18n/I18nProvider';
import { cn, FOCUS_RING } from '@/lib/utils';
import { escFree } from '../canvas/escFree';
import { useOpenStepDetails } from '../canvas/useOpenStepDetails';
import type { ApiDAGDetails } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useWorkspaceUrl, type DockTab } from '../state/workspaceUrl';
import { Z } from '../zLayers';
import {
  clampDockHeight,
  DOCK_MIN_HEIGHT,
  maxDockHeight,
  useDock,
  useDockFrame,
} from './DockContext';
import { DockHeaderToolsContext } from './DockHeaderTools';
import { FreezeWhenHidden } from './FreezeWhenHidden';
import { DOCK_PANEL_ID } from './DockTabStrip';
import { RunStepsNav } from './RunStepsNav';
import { RunTabsPane } from './RunTabsPane';
import { RunTimeline } from './RunTimeline';
import { dockTabBodies } from './tabs';
import { STEP_TAB_LABELS, stepViewTabs } from './tabs/stepScope';
import { useDockAutoOpen } from './useDockAutoOpen';
import { useDockStep } from './useDockStep';

const KEYBOARD_STEP_PX = 40;
// A drag on the sheet handle longer than this closes or maximizes the sheet.
const SHEET_DRAG_PX = 64;

/** True when the media query matches. Without matchMedia it is true. */
function useMinWidth(px: number): boolean {
  const query = `(min-width: ${px}px)`;
  const subscribe = useCallback(
    (onChange: () => void) => {
      const list = window.matchMedia?.(query);
      list?.addEventListener?.('change', onChange);
      return () => list?.removeEventListener?.('change', onChange);
    },
    [query]
  );
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia?.(query).matches ?? true
  );
}

interface Drag {
  pointerId: number;
  startY: number;
  startHeight: number;
}

/**
 * The Run panel at the bottom of the workspace, collapsed at load. A 32px
 * bar holds its toggle, named Activity; aria-expanded tells its state.
 * Under it, while it is open: the timeline of the latest runs, then two
 * columns, the steps of the selected run on the left and on the right the
 * tab strip with the body of the active tab. The body mounts at the first
 * open and stays mounted when the panel collapses or maximizes. Below 768px
 * the open panel is a bottom sheet at 75vh; when it closes, the focus goes
 * back to the element that opened it. Esc restores a maximized panel (and
 * closes the sheet) when no dialog, menu or node details view is above it.
 */
export function LogsDock({
  fileName,
  dag,
}: {
  fileName: string;
  dag: ApiDAGDetails;
}) {
  const { ts } = useI18n();
  const dock = useDock();
  const frame = useDockFrame();
  const run = useWorkspaceRun();
  const [url] = useWorkspaceUrl();
  const activePanel = useActiveRightPanel();
  const openStepDetails = useOpenStepDetails();
  const narrow = !useMinWidth(768);
  const sheet = narrow && dock.open;
  useDockAutoOpen();

  // A step view has no Timeline and names Outputs "Output" (stepScope.ts).
  const step = useDockStep();
  const stepScoped = step !== null;
  const tabs = useMemo(
    () =>
      (stepScoped ? stepViewTabs(frame.tabs) : frame.tabs).filter(
        (tab) => dockTabBodies[tab]
      ),
    [frame.tabs, stepScoped]
  );
  const shown = tabs.includes(dock.tab) ? dock.tab : 'log';
  const Body = dockTabBodies[shown];

  const { setTab } = dock;
  const selectTab = useCallback(
    (tab: DockTab) => setTab(tab, { user: true }),
    [setTab]
  );
  const [mounted, setMounted] = useState(dock.open);
  if (dock.open && !mounted) setMounted(true);

  const drag = useRef<Drag | null>(null);
  const [dragHeight, setDragHeight] = useState<number | null>(null);
  const height = dragHeight ?? dock.height;

  const endDrag = (event: PointerEvent<HTMLDivElement>) => {
    const current = drag.current;
    if (current?.pointerId !== event.pointerId) return;
    drag.current = null;
    setDragHeight(null);
    if (event.clientY !== current.startY) {
      frame.resize(current.startHeight + current.startY - event.clientY);
    }
  };

  const onSeparatorKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const next = {
      ArrowUp: dock.height + KEYBOARD_STEP_PX,
      ArrowDown: dock.height - KEYBOARD_STEP_PX,
      Home: DOCK_MIN_HEIGHT,
      End: maxDockHeight(),
    }[event.key];
    if (next === undefined) return;
    event.preventDefault();
    frame.resize(next);
  };

  // The sheet takes the focus when it opens and gives it back when it closes.
  // The focus goes to the sheet itself, not to Close: a focused button opens
  // its tooltip, and the tooltip would take the first Esc.
  const sheetRef = useRef<HTMLElement>(null);
  const opener = useRef<HTMLElement | null>(null);
  const wasSheet = useRef(sheet);
  useLayoutEffect(() => {
    if (wasSheet.current === sheet) return;
    wasSheet.current = sheet;
    if (sheet) {
      const focused = document.activeElement;
      opener.current = focused instanceof HTMLElement ? focused : null;
      sheetRef.current?.focus();
      return;
    }
    const back = opener.current;
    opener.current = null;
    if (back?.isConnected) back.focus();
  }, [sheet]);

  const sheetDrag = useRef<{ pointerId: number; startY: number } | null>(null);
  const dragged = useRef(false);
  const endSheetDrag = (event: PointerEvent<HTMLDivElement>) => {
    const current = sheetDrag.current;
    if (current?.pointerId !== event.pointerId) return;
    sheetDrag.current = null;
    const moved = event.clientY - current.startY;
    if (Math.abs(moved) < SHEET_DRAG_PX) return;
    dragged.current = true;
    if (moved > 0) dock.toggle();
    else dock.maximize(true);
  };

  const ndvAbove = activePanel === 'ndv';
  const escState = useRef({ ndvAbove, dock, sheet });
  useEffect(() => {
    escState.current = { ndvAbove, dock, sheet };
  });
  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const {
        ndvAbove: blocked,
        dock: current,
        sheet: isSheet,
      } = escState.current;
      if (!escFree(event) || blocked) return;
      if (current.maximized) {
        event.preventDefault();
        current.maximize(false);
      } else if (isSheet) {
        event.preventDefault();
        current.toggle();
      }
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, []);

  const [toolsEl, setToolsEl] = useState<HTMLElement | null>(null);
  const tabOnly = !dock.open && !sheet;
  const ToggleIcon = dock.open ? ChevronDown : ChevronUp;
  const MaximizeIcon = dock.maximized ? Minimize2 : Maximize2;

  return (
    <DockHeaderToolsContext.Provider value={toolsEl}>
      <section
        ref={sheetRef}
        tabIndex={sheet ? -1 : undefined}
        aria-label={ts('Logs')}
        data-testid="ww-dock"
        data-file-name={fileName}
        data-state={dock.open ? 'open' : 'collapsed'}
        data-maximized={dock.maximized || undefined}
        data-sheet={sheet || undefined}
        className={cn(
          'flex flex-col bg-card outline-none',
          Z.dock,
          sheet
            ? 'fixed inset-x-0 bottom-0 rounded-t-lg border-t shadow-lg'
            : 'relative',
          // Collapsed, the panel has no bar: only its tab shows, over the
          // bottom edge of the zone above.
          !dock.open && (sheet ? 'h-8 shrink-0' : 'h-0 shrink-0'),
          dock.open && !dock.maximized && 'shrink-0',
          dock.maximized && 'min-h-0'
        )}
        style={
          sheet
            ? { height: dock.maximized ? '100dvh' : '75vh' }
            : dock.open
              ? { height: dock.maximized ? '100%' : `${height}px` }
              : undefined
        }
      >
        {sheet && (
          // The grip is for the pointer. The Close button below is the named,
          // keyboard-reachable control.
          <div
            aria-hidden="true"
            data-testid="ww-sheet-grip"
            className="flex h-5 w-full shrink-0 touch-none items-center justify-center"
            onPointerDown={(event) => {
              if (event.button !== 0) return;
              // A drag can end with no click: the flag must not skip a tap.
              dragged.current = false;
              event.currentTarget.setPointerCapture(event.pointerId);
              sheetDrag.current = {
                pointerId: event.pointerId,
                startY: event.clientY,
              };
            }}
            onPointerUp={endSheetDrag}
            onPointerCancel={() => {
              sheetDrag.current = null;
            }}
            onClick={() => {
              if (dragged.current) {
                dragged.current = false;
                return;
              }
              dock.toggle();
            }}
          >
            <span className="h-1 w-10 rounded-full bg-muted-foreground/40" />
          </div>
        )}
        {dock.open && !dock.maximized && !sheet && (
          <div
            role="separator"
            aria-label={ts('Resize logs')}
            aria-orientation="horizontal"
            aria-valuemin={DOCK_MIN_HEIGHT}
            aria-valuemax={maxDockHeight()}
            aria-valuenow={height}
            tabIndex={0}
            title={ts('Resize logs')}
            className="absolute inset-x-0 -top-1 z-10 h-2 cursor-row-resize touch-none outline-none hover:bg-primary/20 focus-visible:bg-primary/30"
            onPointerDown={(event) => {
              if (event.button !== 0) return;
              event.preventDefault();
              event.currentTarget.setPointerCapture(event.pointerId);
              drag.current = {
                pointerId: event.pointerId,
                startY: event.clientY,
                startHeight: dock.height,
              };
            }}
            onPointerMove={(event) => {
              const current = drag.current;
              if (current?.pointerId !== event.pointerId) return;
              setDragHeight(
                clampDockHeight(
                  current.startHeight + current.startY - event.clientY
                )
              );
            }}
            onPointerUp={endDrag}
            onPointerCancel={endDrag}
            onKeyDown={onSeparatorKey}
            onDoubleClick={() => frame.resize(null)}
          />
        )}
        <h2 className="sr-only">{ts('Logs')}</h2>
        <div
          data-testid="ww-dock-bar"
          className={
            tabOnly
              ? 'absolute bottom-0 left-3 z-10 flex h-8 items-center'
              : 'flex h-8 shrink-0 items-center gap-2 border-t border-border px-2'
          }
        >
          <button
            type="button"
            aria-expanded={dock.open}
            aria-controls={DOCK_PANEL_ID}
            aria-label={ts('Activity')}
            title={ts('Activity')}
            onClick={dock.toggle}
            className={cn(
              'inline-flex shrink-0 items-center gap-1 text-xs font-semibold text-foreground',
              tabOnly ? 'h-8' : 'h-6',
              // A tab like the one of a binder, laid flat: it sticks out of
              // the bottom edge, with the name of the panel and its chevron.
              tabOnly
                ? 'rounded-t-lg border border-b-0 border-border bg-card px-3 shadow-sm hover:bg-muted'
                : 'rounded-md px-1.5 hover:bg-muted/60',
              FOCUS_RING
            )}
          >
            <ToggleIcon className="h-3.5 w-3.5" aria-hidden="true" />
            <span aria-hidden="true">{ts('Activity')}</span>
          </button>
          {/* The run timeline puts its controls here, beside Maximize. The run
            ID is only a tooltip: the run header has the facts. It stays in the
            tree while the panel is a tab, hidden: a box that comes and goes
            would cost one more render for each toggle. */}
          <div
            ref={setToolsEl}
            hidden={tabOnly}
            data-testid="ww-dock-header-tools"
            className="flex min-w-0 flex-1 items-center justify-end self-stretch"
            title={run.hasRun ? run.ref?.runId : undefined}
          />
          {sheet && url.step && (
            <button
              type="button"
              aria-label={ts('Open {step}', { step: url.step })}
              title={url.step}
              onClick={() => url.step && openStepDetails(url.step)}
              className="inline-flex h-6 shrink-0 items-center rounded-md border px-2 text-xs font-medium text-foreground hover:bg-muted/60"
            >
              {ts('Open')}
            </button>
          )}
          {dock.open && (
            <IconButton
              size="sm"
              icon={MaximizeIcon}
              label={ts(dock.maximized ? 'Restore' : 'Maximize')}
              pressed={dock.maximized}
              onClick={() => dock.maximize(!dock.maximized)}
            />
          )}
          {sheet && (
            <IconButton
              size="sm"
              icon={X}
              label={ts('Close')}
              onClick={dock.toggle}
            />
          )}
        </div>
        <div
          hidden={!dock.open}
          // Contained: a change inside the body does not touch the page.
          className="flex min-h-0 flex-1 flex-col overflow-hidden [contain:layout_paint]"
        >
          {/* A collapsed panel keeps its last render: its steps and timeline do
            not render for the events of a run while nobody sees them. */}
          <FreezeWhenHidden hidden={!dock.open}>
            {mounted && <RunTimeline />}
            {/* The zone of the two columns: its width decides whether the steps
            show as names or as dots (container query). */}
            <div className="@container flex min-h-0 flex-1">
              {mounted && (
                <nav
                  aria-label={ts('Steps')}
                  data-testid="ww-run-steps-nav"
                  className="w-60 shrink-0 overflow-y-auto border-r border-border @max-[900px]:w-11"
                >
                  <RunStepsNav dag={dag} />
                </nav>
              )}
              <RunTabsPane
                tabs={tabs}
                active={shown}
                badges={frame.badges}
                labels={step === null ? undefined : STEP_TAB_LABELS}
                onSelect={selectTab}
              >
                {mounted && Body && (
                  <Body
                    key={shown}
                    run={run}
                    fileName={fileName}
                    dag={dag}
                    // Only the output and the history are about the step: the
                    // other tabs read the URL, and the run tabs have no step
                    // scope in this panel.
                    step={
                      shown === 'outputs' || shown === 'history'
                        ? (step ?? undefined)
                        : undefined
                    }
                    selectTab={selectTab}
                  />
                )}
              </RunTabsPane>
            </div>
          </FreezeWhenHidden>
        </div>
      </section>
    </DockHeaderToolsContext.Provider>
  );
}
