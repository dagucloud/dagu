// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Check, ChevronDown, Search } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useRaisedLayerClass } from '@/components/ui/dialog';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';

/** One choice of a picker. The empty value is the choice that clears the key. */
export interface PickerItem {
  value: string;
  label: string;
  /** A muted second line, such as the id of a model. */
  detail?: string | undefined;
}

/** A list with more items than this has a search box. */
const SEARCH_FROM = 8;
/** The room that the list keeps between its end and the edge of the window. */
const MARGIN = 8;
const TYPE_AHEAD_MS = 700;
/** The tallest list, in layout px: the scale of the graph applies to it. */
const MAX_HEIGHT = 320;
/** The height of a list before it is measured: a row, and the search box. */
const ROW_HEIGHT = 28;
const SEARCH_HEIGHT = 36;
/** The border of the trigger, in layout px: the list covers it to join the two. */
const BORDER = 1;

// An open picker joins its list: the corners on the side of the list are square,
// and the border has the colour of the focus.
const TRIGGER_CLASS =
  'group nodrag nopan flex h-7 w-full min-w-0 items-center gap-1 rounded-md border border-input bg-card px-2 text-left text-xs font-normal text-foreground shadow-sm transition-colors hover:bg-accent/40 focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20 disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground disabled:hover:bg-muted aria-expanded:border-ring aria-expanded:shadow-none aria-expanded:hover:bg-card data-[side=down]:aria-expanded:rounded-b-none data-[side=up]:aria-expanded:rounded-t-none';

interface Place {
  left: number;
  /** The width in layout px: the one of the trigger, before the scale. */
  width: number;
  /** The scale of the graph under the trigger: 1 outside a canvas. */
  scale: number;
  /** The tallest list in layout px, so that the scaled list fits the window. */
  maxHeight: number;
  /** True when the list is over the trigger: there is no room under it. */
  up: boolean;
  top?: number;
  bottom?: number;
}

/**
 * The place of the list in the window. A canvas scales its nodes with a CSS
 * transform, so the trigger is `scale` times smaller on the screen than its
 * layout box, and the list takes the same scale: it has the layout width of
 * the trigger, and after the scale the width on the screen. It joins the
 * trigger: it is flush under it (or over it, when there is no room under) and
 * covers the border that they share. `height` is the layout height that the
 * whole list needs.
 */
function placeOf(anchor: HTMLElement, height: number): Place {
  const rect = anchor.getBoundingClientRect();
  const layout = anchor.offsetWidth;
  const scale = layout > 0 && rect.width > 0 ? rect.width / layout : 1;
  const width = layout > 0 ? layout : rect.width;
  const left = rect.left;
  const join = BORDER * scale;
  const below = window.innerHeight - rect.bottom + join - MARGIN;
  const above = rect.top + join - MARGIN;
  const up = below < Math.min(MAX_HEIGHT, height) * scale && above > below;
  const room = (up ? above : below) / scale;
  const maxHeight = Math.max(ROW_HEIGHT, Math.min(MAX_HEIGHT, room));
  return up
    ? {
        left,
        width,
        scale,
        maxHeight,
        up,
        bottom: window.innerHeight - rect.top - join,
      }
    : { left, width, scale, maxHeight, up, top: rect.bottom - join };
}

function samePlace(a: Place, b: Place): boolean {
  return (
    a.left === b.left &&
    a.width === b.width &&
    a.scale === b.scale &&
    a.maxHeight === b.maxHeight &&
    a.up === b.up &&
    a.top === b.top &&
    a.bottom === b.bottom
  );
}

function matches(item: PickerItem, query: string): boolean {
  const text = `${item.label}\n${item.detail ?? ''}\n${item.value}`;
  return text.toLowerCase().includes(query.toLowerCase());
}

function PickerList({
  anchor,
  popoverId,
  items,
  value,
  searchLabel,
  emptyText,
  onPick,
  onClose,
  onSide,
}: {
  anchor: HTMLElement;
  popoverId: string;
  items: readonly PickerItem[];
  value: string;
  searchLabel: string;
  emptyText: string;
  onPick(value: string): void;
  onClose(refocus: boolean): void;
  /** Tells the trigger on which side the list is: it joins that side. */
  onSide(up: boolean): void;
}): ReactElement {
  const raised = useRaisedLayerClass();
  const search = items.length > SEARCH_FROM;
  const rootRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const headRef = useRef<HTMLDivElement>(null);
  const typed = useRef({ text: '', at: 0 });
  const [query, setQuery] = useState('');
  const shown = useMemo(
    () =>
      query.trim() === ''
        ? items
        : items.filter((i) => matches(i, query.trim())),
    [items, query]
  );
  const [active, setActive] = useState(() =>
    Math.max(
      0,
      items.findIndex((item) => item.value === value)
    )
  );
  // The layout height of the whole list, measured once it is drawn: the choice
  // between under and over the trigger does not change while the user types.
  const [height, setHeight] = useState(
    () => items.length * ROW_HEIGHT + (search ? SEARCH_HEIGHT : 0) + 10
  );
  const [place, setPlace] = useState<Place>(() => placeOf(anchor, height));
  // Pointer or keys: the active item has a ring only for the keys.
  const [keys, setKeys] = useState(false);
  const optionId = (index: number) => `${popoverId}-o${index}`;
  const current = shown[active] ? optionId(active) : undefined;

  useLayoutEffect(() => {
    if (!listRef.current) return;
    const measured =
      listRef.current.scrollHeight + (headRef.current?.offsetHeight ?? 0) + 2;
    setHeight((prev) => (prev === measured ? prev : measured));
  }, [items]);

  // The canvas moves and zooms under an open list: follow the trigger.
  useLayoutEffect(() => {
    let frame = 0;
    const next = placeOf(anchor, height);
    setPlace((prev) => (samePlace(prev, next) ? prev : next));
    const follow = () => {
      const moved = placeOf(anchor, height);
      setPlace((prev) => (samePlace(prev, moved) ? prev : moved));
      frame = requestAnimationFrame(follow);
    };
    frame = requestAnimationFrame(follow);
    return () => cancelAnimationFrame(frame);
  }, [anchor, height]);

  useLayoutEffect(() => {
    onSide(place.up);
  }, [place.up, onSide]);

  useEffect(() => {
    (searchRef.current ?? listRef.current)?.focus({ preventScroll: true });
  }, []);

  useEffect(() => {
    const outside = (event: Event) => {
      const target = event.target as Node | null;
      if (rootRef.current?.contains(target) || anchor.contains(target)) return;
      onClose(false);
    };
    document.addEventListener('pointerdown', outside, true);
    return () => document.removeEventListener('pointerdown', outside, true);
  }, [anchor, onClose]);

  useEffect(() => {
    listRef.current
      ?.querySelector('[data-active="true"]')
      ?.scrollIntoView?.({ block: 'nearest' });
  }, [active, shown]);

  const move = (to: number) => {
    if (shown.length === 0) return;
    setActive((to + shown.length) % shown.length);
  };

  const typeAhead = (key: string) => {
    const now = Date.now();
    const text =
      now - typed.current.at > TYPE_AHEAD_MS ? key : typed.current.text + key;
    typed.current = { text, at: now };
    const lower = text.toLowerCase();
    const from = text.length === 1 ? active + 1 : active;
    for (let step = 0; step < shown.length; step++) {
      const index = (from + step) % shown.length;
      const item = shown[index]!;
      if (
        item.label.toLowerCase().startsWith(lower) ||
        item.value.toLowerCase().startsWith(lower)
      ) {
        setActive(index);
        return;
      }
    }
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const target = event.target as Node;
    const inList = target === listRef.current || target === searchRef.current;
    // The keys of the canvas (Delete, arrows) stay out of an open list.
    if (event.key !== 'Tab') event.stopPropagation();
    setKeys(true);
    if (event.key === 'Escape') {
      event.preventDefault();
      onClose(true);
      return;
    }
    if (!inList) return;
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        move(active + 1);
        return;
      case 'ArrowUp':
        event.preventDefault();
        move(active - 1);
        return;
      case 'Home':
      case 'End':
        if (target === listRef.current) {
          event.preventDefault();
          move(event.key === 'Home' ? 0 : shown.length - 1);
        }
        return;
      case 'Enter': {
        event.preventDefault();
        const item = shown[active];
        if (item) onPick(item.value);
        return;
      }
    }
    if (
      target === listRef.current &&
      event.key.length === 1 &&
      !event.ctrlKey &&
      !event.metaKey &&
      !event.altKey &&
      (event.key !== ' ' || typed.current.text !== '')
    ) {
      event.preventDefault();
      typeAhead(event.key);
    }
  };

  const style: CSSProperties = {
    position: 'fixed',
    left: place.left,
    ...(place.width > 0 ? { width: place.width, minWidth: place.width } : {}),
    maxHeight: place.maxHeight,
    ...(place.top !== undefined ? { top: place.top } : {}),
    ...(place.bottom !== undefined ? { bottom: place.bottom } : {}),
    ...(place.scale !== 1
      ? {
          transform: `scale(${place.scale})`,
          transformOrigin:
            place.bottom !== undefined ? 'bottom left' : 'top left',
        }
      : {}),
  };
  const down = !place.up;
  return createPortal(
    <div
      ref={rootRef}
      id={popoverId}
      style={style}
      data-testid="ai-picker-popover"
      data-side={down ? 'down' : 'up'}
      className={cn(
        'nodrag nopan z-50 flex flex-col overflow-hidden border border-ring bg-card text-xs text-foreground shadow-md',
        'animate-in fade-in-0 duration-100 motion-reduce:animate-none',
        down ? 'rounded-b-md' : 'rounded-t-md',
        raised
      )}
      onKeyDown={onKeyDown}
      onPointerMove={() => keys && setKeys(false)}
      onDoubleClick={(event) => event.stopPropagation()}
      onBlur={(event) => {
        const next = event.relatedTarget as Node | null;
        if (next && !rootRef.current?.contains(next) && !anchor.contains(next))
          onClose(false);
      }}
    >
      {search && (
        <div ref={headRef} className="relative shrink-0 p-1 pb-0">
          <Search
            className="pointer-events-none absolute left-3 top-1/2 mt-0.5 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <input
            ref={searchRef}
            type="text"
            value={query}
            placeholder={searchLabel}
            aria-label={searchLabel}
            aria-controls={`${popoverId}-list`}
            aria-activedescendant={current}
            autoComplete="off"
            className="h-7 w-full min-w-0 rounded-md border border-input bg-card pl-7 pr-2 text-xs shadow-sm outline-none placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/20"
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
          />
        </div>
      )}
      <ul
        ref={listRef}
        id={`${popoverId}-list`}
        role="listbox"
        tabIndex={-1}
        aria-activedescendant={search ? undefined : current}
        className="min-h-0 flex-1 overflow-y-auto p-1 outline-none"
      >
        {shown.length === 0 && (
          <li
            role="presentation"
            className="flex min-h-7 items-center px-2 text-muted-foreground"
          >
            {emptyText}
          </li>
        )}
        {shown.map((item, index) => (
          <li
            key={item.value}
            id={optionId(index)}
            role="option"
            aria-selected={item.value === value}
            data-active={index === active}
            className={cn(
              'flex min-h-7 cursor-pointer select-none items-center gap-1.5 rounded-sm px-2 py-1',
              index === active && 'bg-accent text-accent-foreground',
              index === active && keys && 'ring-1 ring-inset ring-ring',
              item.value === value && 'font-medium'
            )}
            onMouseDown={(event) => event.preventDefault()}
            onPointerMove={() => index !== active && setActive(index)}
            onClick={() => onPick(item.value)}
          >
            <Check
              className={cn(
                'h-3.5 w-3.5 shrink-0',
                item.value !== value && 'invisible'
              )}
              aria-hidden="true"
            />
            <span className="min-w-0 flex-1">
              <span className="block truncate" title={item.label}>
                {item.label}
              </span>
              {item.detail !== undefined && item.detail !== item.label && (
                <span className="block truncate text-[11px] font-normal text-muted-foreground">
                  {item.detail}
                </span>
              )}
            </span>
          </li>
        ))}
      </ul>
    </div>,
    document.body
  );
}

/**
 * A styled picker in place of a native select: a trigger button that shows the
 * current value, and a list in a portal (a React Flow node cannot clip it)
 * with a check on the current item, the keys of a listbox (arrows, Home, End,
 * Enter, Escape, type-ahead), and a search box when the list is long. The list
 * has the width of the trigger, joins it (flush under it, or over it when
 * there is no room under) and follows the scale of the graph. While it is
 * open, the trigger shows its value and its chevron, turned up, with square
 * corners on the side of the list. The trigger and the list carry
 * `nodrag nopan`, so a click never drags a node.
 */
export function Picker({
  id,
  label,
  items,
  value,
  onSelect,
  disabled = false,
  className,
  testId,
  children,
  searchLabel,
  emptyText,
  title,
  marker,
}: {
  id?: string | undefined;
  /** The accessible name of the trigger, when no label element points to it. */
  label?: string | undefined;
  items: readonly PickerItem[];
  /** The value of the current item. */
  value: string;
  onSelect(value: string): void;
  disabled?: boolean;
  /** Classes of the trigger, such as an amber border. */
  className?: string | undefined;
  testId?: string | undefined;
  /** What the trigger shows: the current value. */
  children: ReactNode;
  searchLabel?: string | undefined;
  emptyText?: string | undefined;
  /** A tooltip for the trigger. */
  title?: string | undefined;
  /**
   * A mark inside the trigger, before the chevron, such as a warning sign. It
   * takes room from the value, never from the width of the trigger.
   */
  marker?: ReactNode;
}): ReactElement {
  const { ts } = useI18n();
  const popoverId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const [up, setUp] = useState(false);
  const shut = useCallback((refocus: boolean) => {
    setOpen(false);
    if (refocus) triggerRef.current?.focus();
  }, []);
  const expanded = open && !disabled;
  return (
    <>
      <button
        ref={triggerRef}
        id={id}
        type="button"
        role="combobox"
        aria-haspopup="listbox"
        aria-expanded={expanded}
        aria-controls={expanded ? popoverId : undefined}
        aria-label={label}
        title={title}
        disabled={disabled}
        data-testid={testId}
        data-side={expanded ? (up ? 'up' : 'down') : undefined}
        className={cn(TRIGGER_CLASS, className)}
        onClick={() => setOpen((was) => !was)}
        onKeyDown={(event) => {
          if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            setOpen(true);
          }
        }}
      >
        <span className="flex min-w-0 flex-1 items-baseline gap-1 overflow-hidden">
          {children}
        </span>
        {marker}
        <ChevronDown
          className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform duration-100 group-aria-expanded:rotate-180 motion-reduce:transition-none"
          aria-hidden="true"
        />
      </button>
      {expanded && triggerRef.current && (
        <PickerList
          anchor={triggerRef.current}
          popoverId={popoverId}
          items={items}
          value={value}
          searchLabel={searchLabel ?? ts('Search the models')}
          emptyText={emptyText ?? ts('No model matches.')}
          onPick={(next) => {
            shut(true);
            if (next !== value) onSelect(next);
          }}
          onClose={shut}
          onSide={setUp}
        />
      )}
    </>
  );
}
