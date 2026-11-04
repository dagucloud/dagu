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

/** The narrowest width that the resize handle gives a panel. */
export const PANEL_MIN_WIDTH = 420;
/**
 * The width of its zone that a resized panel leaves uncovered, so that the
 * canvas and the resize handle stay in reach. The rest of the zone is the
 * panel's to take, however wide the screen.
 */
const PANEL_ZONE_MARGIN = 240;

/**
 * The widest that the resize handle makes a panel in a zone of `room` px:
 * all of the zone but PANEL_ZONE_MARGIN. Given a viewport below
 * PANEL_CAP_VIEWPORT, it is the cap of a panel beside the page instead
 * (see panelWidth). A modal panel gives no viewport.
 */
export function panelMaxWidthIn(room: number, viewport?: number): number {
  if (viewport !== undefined && viewport < PANEL_CAP_VIEWPORT) {
    return Math.round(Math.max(PANEL_CAP_RATIO * room, PANEL_CAP_FLOOR));
  }
  return Math.max(PANEL_MIN_WIDTH, Math.round(room) - PANEL_ZONE_MARGIN);
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

/** The cap of panelMaxWidthIn in CSS, against the containing block. */
const ZONE_CAP = `max(100% - ${PANEL_ZONE_MARGIN}px, ${PANEL_MIN_WIDTH}px)`;

function currentViewport(): number {
  return typeof window === 'undefined' ? Infinity : window.innerWidth;
}

/**
 * The CSS width of a panel: the stored width, never wider than the window
 * nor than panelMaxWidthIn its zone (the nearest positioned ancestor).
 * Below PANEL_CAP_VIEWPORT it is capped at PANEL_CAP_RATIO of the zone
 * instead, with PANEL_CAP_FLOOR as its minimum.
 */
export function panelWidth(
  width: number,
  viewport = currentViewport()
): string {
  if (viewport >= PANEL_CAP_VIEWPORT) {
    return `min(100vw, ${width}px, ${ZONE_CAP})`;
  }
  return `min(100vw, ${width}px, max(${PANEL_CAP_RATIO * 100}%, ${PANEL_CAP_FLOOR}px))`;
}

/** The max-width of a panel that sets its own width, as panelWidth caps it. */
export function panelMaxWidth(viewport = currentViewport()): string {
  if (viewport >= PANEL_CAP_VIEWPORT) return ZONE_CAP;
  return `max(${PANEL_CAP_RATIO * 100}%, ${PANEL_CAP_FLOOR}px)`;
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
  children,
}: {
  title?: ReactNode;
  subtitle?: ReactNode;
  closeLabel: string;
  onClose(): void;
  children?: ReactNode;
}): ReactElement {
  return (
    <header data-panel-header className={PANEL_HEADER_CLASS}>
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
