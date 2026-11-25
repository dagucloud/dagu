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
 * The zone also sets --ww-close-slot, the right padding of the row: 8px, or
 * the width that the Close button of a panel keeps at the right end of the
 * row while it is there (PanelRowTitle), so that the toolbars end before it.
 * Each zone sets its own value: a zone nested in another never inherits the
 * slot of a panel in the outer one.
 */
export const TOOLBAR_ZONE_CLASS =
  '[--ww-toolbar-row:2.5rem] max-md:[--ww-toolbar-row:0px] [--ww-close-slot:0.5rem] has-[[data-panel-close=row]]:[--ww-close-slot:2.5rem]';

/**
 * Classes of the row of the toolbars: one line of 40px over the top of the
 * zone, never wrapped, with no background. The row lets a click through to
 * the canvas; each group takes its own (see TOOLBAR_GROUP_CLASS). Its right
 * padding is --ww-close-slot, which its zone sets (TOOLBAR_ZONE_CLASS).
 */
export const TOOLBAR_ROW_CLASS =
  'pointer-events-none absolute inset-x-0 top-0 z-10 flex h-10 min-w-0 items-center justify-between gap-2 pl-2 pr-(--ww-close-slot) max-md:hidden';

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
/** The left padding of the title in its band (pl-4). */
const TITLE_INSET = 16;

/** Room in px that the groups of the host row take from the title of a panel. */
interface RowClearance {
  /**
   * How far the first group, the buttons at the left end of the row, reaches
   * past the left padding of the title, the gap included; 0 when it ends
   * before.
   */
  start: number;
  /**
   * The width of the last group, the buttons at the right end of the row;
   * null while unknown.
   */
  end: number | null;
}

const NO_CLEARANCE: RowClearance = { start: 0, end: null };

/**
 * The room that the groups of the host row take from `band`, the title of a
 * panel under the row. It follows the groups when their buttons change, and
 * the row and the band when they resize, which moves the left edge of the
 * panel under the first group. The band spans the panel and takes the result
 * as its padding: the result never resizes it, nor notifies the observer.
 */
function useRowClearance(
  host: HTMLElement | null | undefined,
  band: HTMLElement | null
): RowClearance {
  const [clearance, setClearance] = useState(NO_CLEARANCE);
  useLayoutEffect(() => {
    if (!host || !band || typeof ResizeObserver !== 'function') return;
    const measure = () => {
      const first = host.firstElementChild;
      const last = host.lastElementChild;
      const start = first
        ? Math.max(
            0,
            Math.round(
              first.getBoundingClientRect().right +
                TITLE_GAP -
                (band.getBoundingClientRect().left + TITLE_INSET)
            )
          )
        : 0;
      const end = last ? Math.round(last.getBoundingClientRect().width) : null;
      setClearance((current) =>
        current.start === start && current.end === end
          ? current
          : { start, end }
      );
    };
    const sizes = new ResizeObserver(measure);
    const watch = () => {
      sizes.disconnect();
      sizes.observe(host);
      // The border box: the padding that the result sets does not count.
      sizes.observe(band, { box: 'border-box' });
      if (host.firstElementChild) sizes.observe(host.firstElementChild);
      if (host.lastElementChild) sizes.observe(host.lastElementChild);
    };
    watch();
    const children = new MutationObserver(watch);
    children.observe(host, { childList: true });
    return () => {
      sizes.disconnect();
      children.disconnect();
    };
  }, [host, band]);
  return clearance;
}

/** Width in px that the Close button of a panel keeps at the end of the row. */
const CLOSE_SLOT = 40;

/**
 * The title of a panel in the row (usePanelInToolbarRow): it stands at the
 * left end of the row band, where the title bar was, after the buttons of
 * the row that reach over the panel, and ends before the buttons at the
 * right end. It hides when less than 4rem is left (16px of padding and room
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
  const [band, setBand] = useState<HTMLDivElement | null>(null);
  const { start, end } = useRowClearance(host, band);
  if (!inRow) return null;
  const withClose = !!onClose && !!closeLabel;
  const style: CSSProperties = {
    paddingLeft: start,
    paddingRight:
      (withClose ? CLOSE_SLOT : 0) + (end === null ? 0 : end + TITLE_GAP),
  };
  return (
    <>
      {title !== undefined && (
        <div
          ref={setBand}
          data-testid="panel-row-title"
          style={style}
          className="pointer-events-none absolute inset-x-0 top-0 z-0 flex h-(--ww-toolbar-row) items-center @container max-md:hidden"
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
