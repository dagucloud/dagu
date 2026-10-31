// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useLayoutEffect, useRef, type ReactNode } from 'react';
import { cn } from '@/lib/utils';
import { useDock } from '../dock/DockContext';

/** The workspace fills its frame; the zones scroll inside, never the page. */
export const ROOT_CLASS = 'flex h-full min-h-0 w-full min-w-0 flex-col';

/**
 * An open dock gives up height before the main zone goes under its floor.
 * The rule overrides the shrink-0 of LogsDock from the parent. A collapsed
 * dock keeps no height: only its tab shows, over the bottom of the zone.
 */
export const DOCK_YIELDS_CLASS =
  '[&>[data-testid=ww-dock]]:shrink [&>[data-testid=ww-dock][data-state=open]]:min-h-8';

/** The zone of the canvas. No minimap below md. */
export const MAIN_ZONE_CLASS =
  'flex min-h-0 min-w-0 flex-1 flex-col max-md:[&_[data-testid=flow-minimap]]:hidden';

/**
 * Keeps the main zone (the canvas, the notice line and its gap) at 280px or
 * more on a window of 640px or more in height. An open dock yields to this
 * floor, so that it can take about 60% of a 900px window. Off while
 * the dock is maximized, so that the dock can take the full height.
 */
export const MAIN_ZONE_FLOOR_CLASS = '[@media(min-height:640px)]:min-h-[280px]';

/**
 * The docked step details are fixed to the window (NodeDetailsFrame). Here
 * they start at the top of the main zone, so they never cover the bar and
 * its Run controls. The sheet below md keeps the full window.
 */
export const NDV_BELOW_BAR_CLASS =
  '[&_[data-testid=ndv][data-state=docked]:not([data-layout=sheet])]:top-(--ww-zone-top)';

/**
 * The expanded step details (NodeDetailsFrame) are fixed to the window and
 * start at --ndv-top, which PanelShell reads. It is the top of the main zone,
 * the same value as --ww-zone-top, so that the bar stays in reach.
 */
const NDV_TOP_VAR = '--ndv-top';

/**
 * Keeps --ww-zone-top and --ndv-top on the root at the top of the main zone in
 * the window.
 */
function useZoneTop() {
  const rootRef = useRef<HTMLDivElement>(null);
  const zoneRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const root = rootRef.current;
    const zone = zoneRef.current;
    if (!root || !zone) return;
    const update = () => {
      const top = Math.max(0, zone.getBoundingClientRect().top);
      root.style.setProperty('--ww-zone-top', `${top}px`);
      root.style.setProperty(NDV_TOP_VAR, `${top}px`);
    };
    update();
    // A change above the zone also changes the height of the root.
    const observer =
      typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(update);
    observer?.observe(root);
    window.addEventListener('resize', update);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', update);
    };
  }, []);
  return { rootRef, zoneRef };
}

export interface WorkspaceLayoutProps {
  bar: ReactNode;
  main: ReactNode;
  dock: ReactNode;
}

/** The zones of the workspace from top to bottom: bar, main zone, dock. */
export function WorkspaceLayout({ bar, main, dock }: WorkspaceLayoutProps) {
  const { maximized } = useDock();
  const { rootRef, zoneRef } = useZoneTop();
  return (
    <div
      ref={rootRef}
      data-testid="workflow-workspace"
      className={cn(ROOT_CLASS, DOCK_YIELDS_CLASS, NDV_BELOW_BAR_CLASS)}
    >
      {bar}
      <div
        ref={zoneRef}
        data-testid="ww-zone-main"
        className={cn(MAIN_ZONE_CLASS, !maximized && MAIN_ZONE_FLOOR_CLASS)}
      >
        {main}
      </div>
      {dock}
    </div>
  );
}
