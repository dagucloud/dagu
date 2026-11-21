// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { X } from 'lucide-react';
import {
  createContext,
  useContext,
  useEffect,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { cn } from '@/lib/utils';

/** The placement that every right-side panel shares. */
export const PANEL_DOCK_GEOMETRY =
  'bottom-0 right-0 top-[var(--ndv-top,0px)] max-w-full border-l';

/**
 * Where the panels sit. 'window' fixes them to the window, under the bar that
 * the host sets in `--ndv-top`. 'zone' puts them inside the nearest positioned
 * ancestor, the graph zone of the workspace, and never beyond it. In a zone
 * they start under its row of toolbars (`--ww-toolbar-row`).
 */
export type PanelPlacement = 'window' | 'zone';

const PanelPlacementContext = createContext<PanelPlacement>('window');

/** A host gives 'zone' to the panels below it; without it they use the window. */
export const PanelPlacementProvider = PanelPlacementContext.Provider;

export function usePanelPlacement(): PanelPlacement {
  return useContext(PanelPlacementContext);
}

/**
 * The right column of a host: the element that each panel of the page (the
 * Workflow panel, the YAML of the file) renders into, beside the zone instead
 * of over it. The host shows one of them and keeps the others mounted. A
 * panel in the column claims no right-panel slot, so no other panel closes
 * it. Null where the host has no column.
 */
export interface PanelColumn {
  workflow: HTMLElement | null;
  yaml: HTMLElement | null;
  /** The temporary tab of the panel of a trigger (a launcher). */
  launcher: HTMLElement | null;
  /**
   * A layer over the graph zone and the column. The YAML frame stays in it,
   * over the YAML tab or over both when it shows a step, so that its editor
   * is never mounted again when it changes place.
   */
  layer: HTMLElement | null;
}

const PanelColumnContext = createContext<PanelColumn | null>(null);

/** A host with a right column gives its elements to the panels below it. */
export const PanelColumnProvider = PanelColumnContext.Provider;

export function usePanelColumn(): PanelColumn | null {
  return useContext(PanelColumnContext);
}

// The important top wins over a host rule that starts a fixed panel below its bar.
// The zone sets --ww-toolbar-row to the height of its row of toolbars (0 without
// one). A panel in the zone is one box from the top of the zone: the first rows
// of its padding are under the toolbars, which sit over it, so that the row and
// the title bar have the one background of the panel.
export const ZONE_PANEL_TOP = 'top-0! pt-(--ww-toolbar-row,0px)';
const ZONE_DOCK_GEOMETRY = `bottom-0 right-0 ${ZONE_PANEL_TOP} max-w-full border-l`;

const SHELL_BASE =
  'z-[60] flex flex-col border-border bg-card shadow-lg shadow-foreground/10';

/** The edges of a docked panel for a placement. */
export function panelDockGeometry(placement: PanelPlacement): string {
  return placement === 'zone' ? ZONE_DOCK_GEOMETRY : PANEL_DOCK_GEOMETRY;
}

/**
 * One geometry for every panel on the right of the page (Workflow, YAML, Add
 * a step, Step details). The panel starts under the bar that the host sets in
 * `--ndv-top` (0 without a host), ends at the bottom of the window, has the
 * stored width, and scrolls its body, never itself.
 */
export const PANEL_SHELL_CLASS = `fixed ${SHELL_BASE} ${PANEL_DOCK_GEOMETRY}`;

/** The same shell inside the graph zone: absolute, on the right, from under the row of toolbars to the bottom. */
export const PANEL_ZONE_SHELL_CLASS = `absolute ${SHELL_BASE} ${ZONE_DOCK_GEOMETRY}`;

/** The shell class of a panel for a placement. */
export function panelShellClass(placement: PanelPlacement): string {
  return placement === 'zone' ? PANEL_ZONE_SHELL_CLASS : PANEL_SHELL_CLASS;
}

/** The title bar: one height for every panel, the close button at its end. */
export const PANEL_HEADER_CLASS =
  'flex h-12 shrink-0 items-center gap-2 border-b border-border bg-card px-3';

/** The scrolling body under the title bar. */
export const PANEL_BODY_CLASS = 'min-h-0 flex-1 overflow-auto p-4';

/** Below this viewport width a panel never takes more than its share of the zone. */
export const PANEL_CAP_VIEWPORT = 1100;
/** The share of the zone that a panel may take on a narrow viewport. */
export const PANEL_CAP_RATIO = 0.45;
/** A capped panel keeps this width, so that its form stays usable. */
export const PANEL_CAP_FLOOR = 360;
/** On a wide viewport the graph beside a panel keeps this width at least. */
export const PANEL_GRAPH_FLOOR = 360;

/** The narrowest width that the resize handle gives a panel. */
export const PANEL_MIN_WIDTH = 420;
/**
 * The widest that the resize handle makes a panel in a zone of `room` px:
 * the zone less PANEL_GRAPH_FLOOR, which the graph keeps, and never under
 * PANEL_CAP_FLOOR. Given a viewport below PANEL_CAP_VIEWPORT, it is the cap
 * of a panel beside the page instead (see panelWidth). A modal panel gives
 * no viewport.
 */
export function panelMaxWidthIn(room: number, viewport?: number): number {
  if (viewport !== undefined && viewport < PANEL_CAP_VIEWPORT) {
    return Math.round(Math.max(PANEL_CAP_RATIO * room, PANEL_CAP_FLOOR));
  }
  return Math.max(PANEL_CAP_FLOOR, Math.round(room) - PANEL_GRAPH_FLOOR);
}

/** A width that the resize handle allows, with the bounds of panelMaxWidthIn. */
export function clampPanelWidth(
  width: number,
  room: number,
  viewport?: number
): number {
  const max = panelMaxWidthIn(room, viewport);
  const min = Math.min(PANEL_MIN_WIDTH, max);
  return Math.round(Math.min(Math.max(width, min), max));
}

function currentViewport(): number {
  return typeof window === 'undefined' ? Infinity : window.innerWidth;
}

/** The CSS cap of a panel: its share of the zone, with a floor. */
function capWidth(viewport: number): string {
  return viewport >= PANEL_CAP_VIEWPORT
    ? `max(100% - ${PANEL_GRAPH_FLOOR}px, ${PANEL_CAP_FLOOR}px)`
    : `max(${PANEL_CAP_RATIO * 100}%, ${PANEL_CAP_FLOOR}px)`;
}

/**
 * The CSS width of a panel: the stored width, never wider than the window.
 * Below PANEL_CAP_VIEWPORT it is also capped at PANEL_CAP_RATIO of the zone
 * (the nearest positioned ancestor), with PANEL_CAP_FLOOR as its minimum.
 * From PANEL_CAP_VIEWPORT the zone keeps PANEL_GRAPH_FLOOR for the graph.
 */
export function panelWidth(
  width: number,
  viewport = currentViewport()
): string {
  return `min(100vw, ${width}px, ${capWidth(viewport)})`;
}

/** The max-width of a panel that sets its own width. */
export function panelMaxWidth(viewport = currentViewport()): string {
  return capWidth(viewport);
}

/**
 * The width in pixels that a panel takes in a zone of `zoneWidth`, as
 * panelWidth computes it in CSS. A host that shrinks its content beside the
 * panel reads this value.
 */
export function effectivePanelWidth(
  width: number,
  zoneWidth: number,
  viewport = currentViewport()
): number {
  return Math.min(width, viewport, panelMaxWidthIn(zoneWidth, viewport));
}

/** The viewport width that tracks resizes, for the cap of panelWidth. */
export function useViewportWidth(): number {
  const [width, setWidth] = useState(() =>
    typeof window === 'undefined' ? PANEL_CAP_VIEWPORT : window.innerWidth
  );
  useEffect(() => {
    const update = () => setWidth(window.innerWidth);
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, []);
  return width;
}

/** The title bar of a panel. `children` replaces the title (the YAML bar). */
export function PanelHeader({
  title,
  subtitle,
  closeLabel,
  onClose,
  className,
  children,
}: {
  title?: ReactNode;
  subtitle?: ReactNode;
  closeLabel: string;
  onClose(): void;
  /** Extra classes for the bar. */
  className?: string;
  children?: ReactNode;
}): ReactElement {
  return (
    <header data-panel-header className={cn(PANEL_HEADER_CLASS, className)}>
      {children ?? (
        <div className="min-w-0 flex-1 leading-tight">
          <h2 className="truncate text-base font-semibold text-foreground">
            {title}
          </h2>
          {subtitle && (
            <p className="truncate text-xs text-muted-foreground">{subtitle}</p>
          )}
        </div>
      )}
      <IconButton
        icon={X}
        label={closeLabel}
        className="shrink-0"
        onClick={onClose}
      />
    </header>
  );
}
