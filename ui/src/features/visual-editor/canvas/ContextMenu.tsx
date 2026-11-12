// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Check } from 'lucide-react';
import {
  Fragment,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactElement,
} from 'react';
import { createPortal } from 'react-dom';
import { Kbd } from '@/components/ui/kbd';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { ContextMenuRow, ContextMenuRowId } from './contextMenuItems';

const EDGE_GAP = 4;

/** How a row was chosen: a click, or Enter or Space. */
export type MenuChoice = 'pointer' | 'keyboard';

export interface ContextMenuProps {
  rows: ContextMenuRow[];
  /** Client coordinates of the top-left corner. */
  x: number;
  y: number;
  /** Accessible name of the menu. */
  label: string;
  onSelect(id: ContextMenuRowId, via: MenuChoice): void;
  /** `restoreFocus` is true when the focus goes back to the canvas. */
  onClose(restoreFocus: boolean): void;
}

/** The element that had the focus before the menu took it, if it can take it back. */
function focusOrigin(): HTMLElement | null {
  const element = document.activeElement;
  return element instanceof HTMLElement && element !== document.body
    ? element
    : null;
}

/**
 * A menu at a point of the screen. Arrows, Home and End move a roving focus
 * between the enabled rows, Enter or Space selects, Esc and Tab close. A
 * disabled row tells its reason through its description. Esc and Tab give
 * the focus back to the element that had it when the menu opened, such as
 * the node button or the canvas.
 */
export function ContextMenu({
  rows,
  x,
  y,
  label,
  onSelect,
  onClose,
}: ContextMenuProps): ReactElement {
  const { ts } = useI18n();
  const menuRef = useRef<HTMLDivElement>(null);
  const reasonId = useId();
  const [origin] = useState(focusOrigin);
  const itemRefs = useRef(new Map<ContextMenuRowId, HTMLDivElement>());
  const [place, setPlace] = useState({ left: x, top: y });
  const enabled = rows.filter((item) => !item.disabled).map((item) => item.id);
  // The rows can change while the menu is open (a run updates the gates):
  // the active row is kept by id, so Enter never picks another row.
  const [chosen, setActive] = useState<ContextMenuRowId | null>(
    enabled[0] ?? null
  );
  const active =
    chosen !== null && enabled.includes(chosen) ? chosen : (enabled[0] ?? null);

  // Keep the whole menu on screen.
  useLayoutEffect(() => {
    const menu = menuRef.current;
    if (!menu) {
      return;
    }
    const { width, height } = menu.getBoundingClientRect();
    setPlace({
      left: Math.max(
        EDGE_GAP,
        Math.min(x, window.innerWidth - width - EDGE_GAP)
      ),
      top: Math.max(
        EDGE_GAP,
        Math.min(y, window.innerHeight - height - EDGE_GAP)
      ),
    });
  }, [x, y]);

  useEffect(() => {
    const element =
      (active !== null ? itemRefs.current.get(active) : undefined) ??
      menuRef.current;
    element?.focus();
  }, [active]);

  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        closeRef.current(false);
      }
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () =>
      document.removeEventListener('pointerdown', onPointerDown, true);
  }, []);

  const move = (step: 1 | -1) => {
    if (enabled.length === 0) {
      return;
    }
    const at = active === null ? -1 : enabled.indexOf(active);
    const next =
      at < 0
        ? step === 1
          ? 0
          : enabled.length - 1
        : (at + step + enabled.length) % enabled.length;
    setActive(enabled[next]!);
  };

  const choose = (id: ContextMenuRowId | null, via: MenuChoice) => {
    const item = rows.find((candidate) => candidate.id === id);
    if (item && !item.disabled) {
      onSelect(item.id, via);
    }
  };

  const closeAndRestore = () => {
    onClose(true);
    if (origin?.isConnected && !menuRef.current?.contains(origin)) {
      origin.focus();
    }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    let handled = true;
    switch (event.key) {
      case 'ArrowDown':
        move(1);
        break;
      case 'ArrowUp':
        move(-1);
        break;
      case 'Home':
        if (enabled.length > 0) setActive(enabled[0]!);
        break;
      case 'End':
        if (enabled.length > 0) setActive(enabled[enabled.length - 1]!);
        break;
      case 'Enter':
      case ' ':
        choose(active, 'keyboard');
        break;
      case 'Escape':
      case 'Tab':
        closeAndRestore();
        break;
      default:
        handled = false;
    }
    if (handled) {
      event.preventDefault();
    }
    // The menu is in a portal: keys must not reach the editor around it.
    event.stopPropagation();
  };

  return createPortal(
    <div
      ref={menuRef}
      role="menu"
      aria-label={label}
      tabIndex={-1}
      data-testid="ve-canvas-menu"
      onKeyDown={onKeyDown}
      onContextMenu={(event) => event.preventDefault()}
      className="fixed z-[70] min-w-48 rounded-md border bg-popover p-1 text-sm text-popover-foreground shadow-md outline-none"
      style={{ left: place.left, top: place.top }}
    >
      {rows.map((item, index) => {
        const separated =
          index > 0 && rows[index - 1]!.section !== item.section;
        const radio = item.checked !== undefined;
        const reason = item.disabled && item.reason ? ts(item.reason) : null;
        return (
          <Fragment key={item.id}>
            {separated && (
              <div role="separator" className="-mx-1 my-1 h-px bg-border" />
            )}
            <div
              ref={(element) => {
                if (element) {
                  itemRefs.current.set(item.id, element);
                } else {
                  itemRefs.current.delete(item.id);
                }
              }}
              role={radio ? 'menuitemradio' : 'menuitem'}
              aria-checked={radio ? item.checked : undefined}
              aria-label={ts(item.label)}
              aria-keyshortcuts={item.shortcut || undefined}
              aria-disabled={item.disabled || undefined}
              aria-describedby={reason ? `${reasonId}-${item.id}` : undefined}
              tabIndex={item.id === active ? 0 : -1}
              title={item.reason ? ts(item.reason) : undefined}
              data-row={item.id}
              onClick={() => choose(item.id, 'pointer')}
              onPointerMove={() => {
                if (!item.disabled && active !== item.id) setActive(item.id);
              }}
              className={cn(
                'flex cursor-default select-none items-center gap-2 rounded-sm px-2 py-1.5 outline-none focus:bg-accent focus:text-accent-foreground',
                item.disabled && 'cursor-not-allowed opacity-50'
              )}
            >
              <span className="flex h-3.5 w-3.5 shrink-0 items-center justify-center">
                {item.checked && <Check className="h-3.5 w-3.5" />}
              </span>
              <span className="flex-1">{ts(item.label)}</span>
              {reason && (
                <span id={`${reasonId}-${item.id}`} className="sr-only">
                  {reason}
                </span>
              )}
              {item.shortcut && <Kbd className="ml-4">{item.shortcut}</Kbd>}
            </div>
          </Fragment>
        );
      })}
    </div>,
    document.body
  );
}
