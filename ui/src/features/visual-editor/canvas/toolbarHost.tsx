// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  createContext,
  useContext,
  useLayoutEffect,
  useMemo,
  useState,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
} from 'react';
import { usePanelPlacement } from '@/features/dags/components/step-details/PanelShell';
import { X } from 'lucide-react';
import { IconButton } from '@/components/ui/icon-button';
import { useElementWidth } from '@/hooks/useElementWidth';
import { cn } from '@/lib/utils';

/**
 * The row at the top of a graph zone that holds the toolbars of the canvas.
 * `undefined`: no host, the toolbars float over the canvas. `null`: the host
 * row is not mounted yet. An element: the toolbars render in it.
 */
export const CanvasToolbarHostContext = createContext<
  HTMLElement | null | undefined
>(undefined);

/** The row of the toolbars that the zone gives, if any. */
export function useCanvasToolbarHost(): HTMLElement | null | undefined {
  return useContext(CanvasToolbarHostContext);
}

/**
 * True for a panel that sits in a graph zone with a row of toolbars. Its title
 * and its tools are in the row from md, where the row shows: the panel drops
 * its title bar then (see ROW_PANEL_HEADER_CLASS).
 */
export function usePanelInToolbarRow(): boolean {
  const zone = usePanelPlacement() === 'zone';
  return useCanvasToolbarHost() !== undefined && zone;
}

/**
 * Class of the title bar of a panel in the row (usePanelInToolbarRow). Below
 * md the row is hidden, and the bar stays with its close button.
 */
export const ROW_PANEL_HEADER_CLASS = 'md:hidden';

/**
 * Classes of a graph zone with a row for the toolbars: it sets the height of
 * the row in --ww-toolbar-row, where the right panels read the top that they
 * start at. The row floats over the canvas, which fills the whole zone; the
 * panels start under it. Below md the row is hidden and the variable is 0.
 * While a panel has its Close button in the row (PanelRowTitle), the zone sets
 * --ww-close-slot to the width that the button keeps at the right end of the
 * row: the toolbars end before it. Without such a panel the variable is unset.
 */
export const TOOLBAR_ZONE_CLASS =
  '[--ww-toolbar-row:2.5rem] max-md:[--ww-toolbar-row:0px] has-[[data-panel-close=row]]:[--ww-close-slot:2.5rem]';

/**
 * Classes of the row of the toolbars: one line of 40px over the top of the
 * zone, never wrapped, with no background. The row lets a click through to
 * the canvas; each group takes its own (see TOOLBAR_GROUP_CLASS). Its right
 * padding is 8px, or --ww-close-slot when a panel has its Close button there.
 */
export const TOOLBAR_ROW_CLASS =
  'pointer-events-none absolute inset-x-0 top-0 z-10 flex h-10 min-w-0 items-center justify-between gap-2 pl-2 pr-(--ww-close-slot,0.5rem) max-md:hidden';

/**
 * With a right panel docked in the zone, the row goes above the panel (z 60),
 * so that its controls sit over the top rows of the one box that the panel is.
 */
export const TOOLBAR_ROW_OVER_PANEL_CLASS = 'z-[65]';

/**
 * Classes of a group of toolbar controls in the row: it takes the pointer, has
 * no background, border or shadow of its own, and each of its buttons floats
 * with its own border and a translucent card background. There is no blur:
 * the canvas moves under the row, and a blur would repaint at each frame. A
 * button with a fill of its own (Save, a pressed toggle) keeps it. Literal
 * strings, so that Tailwind finds them.
 */
export const TOOLBAR_GROUP_CLASS = [
  'pointer-events-auto',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true])]:border',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true])]:border-border',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true])]:bg-card/90',
  '[&_button:not([class*=primary-fill]):not([class*=bg-destructive]):not([aria-pressed=true]):hover]:bg-muted',
  '[&_button[aria-pressed=true]]:border',
  '[&_button[aria-pressed=true]]:border-border',
  '[&_[aria-hidden=true].w-px]:hidden',
].join(' ');

/** The width of the host row, kept current; null while unknown. */
export function useToolbarHostWidth(
  host: HTMLElement | null | undefined
): number | null {
  const ref = useMemo(() => ({ current: host ?? null }), [host]);
  return useElementWidth(ref);
}

/** Gap between the title of a panel and the buttons of the row. */
const TITLE_GAP = 12;

/**
 * The width of the last group of the host row, the buttons at its right end;
 * null while unknown. It follows the group when its buttons change.
 */
function useToolbarEndWidth(host: HTMLElement | null | undefined) {
  const [width, setWidth] = useState<number | null>(null);
  useLayoutEffect(() => {
    if (!host || typeof ResizeObserver !== 'function') return;
    const sizes = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1];
      setWidth(entry ? Math.round(entry.contentRect.width) : null);
    });
    const watch = () => {
      sizes.disconnect();
      if (host.lastElementChild) sizes.observe(host.lastElementChild);
    };
    watch();
    const children = new MutationObserver(watch);
    children.observe(host, { childList: true });
    return () => {
      sizes.disconnect();
      children.disconnect();
    };
  }, [host]);
  return width;
}

/** Width in px that the Close button of a panel keeps at the end of the row. */
const CLOSE_SLOT = 40;

/**
 * The title of a panel in the row (usePanelInToolbarRow): it stands at the
 * left end of the row band, where the title bar was, and ends before the
 * buttons. It hides when less than 4rem is left (16px of padding and room
 * for a short word). With `onClose`, the Close button of the panel stands in
 * the band at the right end of the panel, and the toolbars of the row end
 * before it (TOOLBAR_ZONE_CLASS). Put it first in the panel, which is
 * positioned.
 */
export function PanelRowTitle({
  title,
  subtitle,
  leading,
  closeLabel,
  onClose,
}: {
  /** Without it the band has the Close button alone. */
  title?: string;
  /** A smaller line over the title. */
  subtitle?: string;
  /** Before the title, such as a status dot. */
  leading?: ReactNode;
  /** The name of the Close button; given with `onClose`. */
  closeLabel?: string;
  onClose?(): void;
}): ReactElement | null {
  const host = useCanvasToolbarHost();
  const inRow = usePanelInToolbarRow();
  const buttons = useToolbarEndWidth(host);
  if (!inRow) return null;
  const withClose = !!onClose && !!closeLabel;
  const style: CSSProperties = {
    right:
      (withClose ? CLOSE_SLOT : 0) +
      (buttons === null ? 0 : buttons + TITLE_GAP),
  };
  return (
    <>
      {title !== undefined && (
        <div
          data-testid="panel-row-title"
          style={style}
          className="pointer-events-none absolute left-0 top-0 z-0 flex h-(--ww-toolbar-row) items-center @container max-md:hidden"
        >
          <div
            className={cn(
              'hidden h-full min-w-0 flex-1 items-center gap-2 pl-4',
              '@min-[4rem]:flex'
            )}
          >
            {leading}
            <div className="min-w-0 flex-1 leading-tight">
              {subtitle && (
                <p
                  aria-hidden="true"
                  className="truncate text-[11px] text-muted-foreground"
                >
                  {subtitle}
                </p>
              )}
              <h2
                aria-hidden="true"
                className={cn(
                  'truncate font-semibold text-foreground',
                  subtitle ? 'text-sm' : 'text-base'
                )}
              >
                {title}
              </h2>
            </div>
          </div>
        </div>
      )}
      {withClose && (
        // The marker lets the zone keep this slot free of the toolbars. The
        // button keeps its place at every width: the title gives way first.
        <div
          data-panel-close="row"
          className="absolute right-0 top-0 z-0 flex h-(--ww-toolbar-row) w-10 items-center justify-center max-md:hidden"
        >
          <IconButton
            label={closeLabel}
            icon={X}
            className="pointer-events-auto shrink-0"
            onClick={onClose}
          />
        </div>
      )}
    </>
  );
}
