// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronUp, Maximize2, Minimize2 } from 'lucide-react';
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
  DOCK_SHEET_BELOW_PX,
} from './DockContext';
import { DockHeaderToolsContext } from './DockHeaderTools';
import { FreezeWhenHidden } from './FreezeWhenHidden';
import { DOCK_PANEL_ID } from './DockTabStrip';
import { PhoneViewNav } from './PhoneViewNav';
import { RunStepPicker, RunStepsNav } from './RunStepsNav';
import { RunTabsPane } from './RunTabsPane';
import { RunTimeline } from './RunTimeline';
import { dockTabBodies } from './tabs';
import { STEP_TAB_LABELS, stepViewTabs } from './tabs/stepScope';
import { useDockAutoOpen } from './useDockAutoOpen';
import { useDockStep } from './useDockStep';
import { useLiftNotifications } from './useLiftNotifications';

const KEYBOARD_STEP_PX = 40;
const STEPS_NAV_ID = 'ww-run-steps-nav';
// The tabs that most runs do not need: they wait in the More menu.
const RARE_TABS: readonly DockTab[] = ['definition', 'artifacts'];

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
 * tab strip with the body of the active tab. A panel narrower than 600px
 * has one column: a row names the selected step and opens the steps over
 * the tabs. The body mounts at the first
 * open and stays mounted when the panel collapses or maximizes. Below 768px
 * the panel has no bar: the views bar of the phone (PhoneViewNav) is its
 * bottom, and the open panel takes the place of the graph; when it closes,
 * the focus goes back to the element that opened it. Esc restores a
 * maximized panel (and closes the phone view) when no dialog, menu or node
 * details view is above it.
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
  const narrow = !useMinWidth(DOCK_SHEET_BELOW_PX);
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
  const [stepsOpen, setStepsOpen] = useState(false);
  const pickerRef = useRef<HTMLButtonElement>(null);
  // The list over the tabs closes on a pick or on Esc, and gives the focus
  // back to the row that opened it.
  const closeSteps = useCallback(() => {
    if (!stepsOpen) return;
    setStepsOpen(false);
    pickerRef.current?.focus();
  }, [stepsOpen]);
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
  // An open panel or sheet keeps the notifications above it. A maximized one
  // fills the page: they stay in the corner.
  useLiftNotifications(sheetRef, dock.open && !dock.maximized);
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

  const ndvAbove = activePanel === 'ndv';
  const escState = useRef({ ndvAbove, dock, sheet, stepsOpen, closeSteps });
  useEffect(() => {
    escState.current = { ndvAbove, dock, sheet, stepsOpen, closeSteps };
  });
  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const {
        ndvAbove: blocked,
        dock: current,
        sheet: isSheet,
        stepsOpen: listOpen,
        closeSteps: closeList,
      } = escState.current;
      if (!escFree(event) || blocked) return;
      // The step list over the tabs is the top layer: it closes first.
      if (listOpen) {
        event.preventDefault();
        closeList();
      } else if (current.maximized) {
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
  const tabOnly = !dock.open && !narrow;
  const ToggleIcon = dock.open ? ChevronDown : ChevronUp;
  const MaximizeIcon = dock.maximized ? Minimize2 : Maximize2;

  return (
    // A phone has no bar for the controls of the timeline: the timeline
    // draws them in its own row.
    <DockHeaderToolsContext.Provider value={narrow ? undefined : toolsEl}>
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
          'relative flex flex-col bg-card outline-none',
          Z.dock,
          // On a phone the open panel takes the place of the graph
          // (WorkspaceLayout hides it), above the views bar.
          sheet && 'min-h-0 flex-1',
          // Collapsed, the panel has no bar: only its tab shows, over the
          // bottom edge of the zone above; on a phone, the views bar.
          !dock.open && (narrow ? 'shrink-0' : 'h-0 shrink-0'),
          dock.open && !dock.maximized && !sheet && 'shrink-0',
          dock.maximized && !sheet && 'min-h-0'
        )}
        style={
          dock.open && !sheet
            ? { height: dock.maximized ? '100%' : `${height}px` }
            : undefined
        }
      >
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
        {sheet && url.step && (
          <div className="flex h-9 shrink-0 items-center justify-end border-b border-border px-2">
            <button
              type="button"
              aria-label={ts('Open {step}', { step: url.step })}
              title={url.step}
              onClick={() => url.step && openStepDetails(url.step)}
              className="inline-flex h-7 shrink-0 items-center rounded-md border px-2.5 text-xs font-medium text-foreground hover:bg-muted/60"
            >
              {ts('Open')}
            </button>
          </div>
        )}
        <div
          hidden={narrow}
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
          {dock.open && (
            <IconButton
              size="sm"
              icon={MaximizeIcon}
              label={ts(dock.maximized ? 'Restore' : 'Maximize')}
              pressed={dock.maximized}
              onClick={() => dock.maximize(!dock.maximized)}
            />
          )}
        </div>
        <div
          hidden={!dock.open}
          // Contained: a change inside the body does not touch the page. Its
          // width decides whether the steps fit beside the tabs (container
          // query).
          className="@container flex min-h-0 flex-1 flex-col overflow-hidden [contain:layout_paint]"
        >
          {/* A collapsed panel keeps its last render: its steps and timeline do
            not render for the events of a run while nobody sees them. */}
          <FreezeWhenHidden hidden={!dock.open}>
            {mounted && <RunTimeline compact={narrow} />}
            <div className="relative flex min-h-0 flex-1 @max-[600px]:flex-col">
              {mounted && (
                <RunStepPicker
                  ref={pickerRef}
                  open={stepsOpen}
                  controls={STEPS_NAV_ID}
                  onToggle={() => setStepsOpen((open) => !open)}
                  className="hidden @max-[600px]:flex"
                />
              )}
              {mounted && (
                <nav
                  id={STEPS_NAV_ID}
                  aria-label={ts('Steps')}
                  data-testid="ww-run-steps-nav"
                  className={cn(
                    'w-60 shrink-0 overflow-y-auto border-r border-border @max-[900px]:w-44',
                    // Over the tabs, under the row that opens it.
                    '@max-[600px]:absolute @max-[600px]:inset-x-0 @max-[600px]:bottom-0 @max-[600px]:top-8 @max-[600px]:z-10 @max-[600px]:w-auto @max-[600px]:border-r-0 @max-[600px]:bg-card',
                    !stepsOpen && '@max-[600px]:hidden'
                  )}
                >
                  <RunStepsNav dag={dag} onPicked={closeSteps} />
                </nav>
              )}
              <RunTabsPane
                tabs={tabs}
                active={shown}
                badges={frame.badges}
                labels={step === null ? undefined : STEP_TAB_LABELS}
                overflow={RARE_TABS}
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
        {narrow && <PhoneViewNav />}
      </section>
    </DockHeaderToolsContext.Provider>
  );
}
