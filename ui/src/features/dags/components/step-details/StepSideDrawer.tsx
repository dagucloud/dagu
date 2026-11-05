// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cn } from '@/lib/utils';
import React from 'react';
import { createPortal } from 'react-dom';
import {
  useRightPanelClaim,
  type RightPanelId,
} from '@/contexts/RightPanelContext';
import { I18nProps } from '@/i18n/I18nProps';
import { useI18n } from '@/i18n/I18nProvider';
import {
  clampPanelWidth,
  PANEL_MIN_WIDTH,
  panelMaxWidthIn,
  panelShellClass,
  panelWidth,
  usePanelPlacement,
  useViewportWidth,
} from './PanelShell';

type StepSideDrawerProps = {
  isOpen: boolean;
  onClose: () => void;
  /** False while there is nothing to show, so that the drawer closes. */
  hasContent: boolean;
  /**
   * A modal drawer covers the page with a layer that closes it, traps the
   * focus and hides the page from screen readers. Without it, the page stays
   * usable beside the drawer.
   */
  modal?: boolean;
  /** Title bar; the drawer has none when it is omitted. */
  header?: React.ReactNode;
  /** The `id` of the element that names the drawer. */
  labelledBy?: string;
  /** Names the drawer when no element does. */
  label?: string;
  /**
   * The right-side panel that the drawer is. While it is open, it claims the
   * id, and another panel that claims its place closes it.
   */
  panelId?: RightPanelId;
  /** Opens the drawer again when a transient panel that covered it closes. */
  onRestore?: () => void;
  /**
   * Beside the page, Escape also closes the drawer while the focus is on
   * the page, such as the button that opened it, but not in a text field.
   */
  closeOnPageEscape?: boolean;
  children: React.ReactNode;
};

/** True for a field or an editor that keeps Escape for itself. */
function keepsEscape(element: Element | null): boolean {
  if (!(element instanceof HTMLElement)) return false;
  return (
    element.isContentEditable ||
    ['INPUT', 'TEXTAREA', 'SELECT'].includes(element.tagName) ||
    !!element.closest('.monaco-editor, [role="dialog"], [role="menu"]')
  );
}

const DRAWER_WIDTH_STORAGE_KEY = 'dagu.stepDetailsDrawer.width';
const DEFAULT_DRAWER_WIDTH = 560;
/** The change of width of one arrow key on the resize handle. */
const DRAWER_WIDTH_STEP = 40;

/** A stored width inside the bounds of a panel that fills the window. */
function clampDrawerWidth(value: number): number {
  return clampPanelWidth(value, window.innerWidth);
}

/** The width the user chose for the drawer, kept between visits. */
export function getStoredDrawerWidth(): number {
  if (typeof window === 'undefined') {
    return DEFAULT_DRAWER_WIDTH;
  }

  let storedValue: string | null = null;
  try {
    storedValue = window.localStorage.getItem(DRAWER_WIDTH_STORAGE_KEY);
  } catch {
    return DEFAULT_DRAWER_WIDTH;
  }

  const parsedValue = storedValue ? Number(storedValue) : DEFAULT_DRAWER_WIDTH;
  return Number.isFinite(parsedValue)
    ? clampDrawerWidth(parsedValue)
    : DEFAULT_DRAWER_WIDTH;
}

// The width while the user drags a handle, before it is stored, and the
// width that the storage refused: every right panel and the layout beside
// them follow it (see useDrawerWidth).
let liveDrawerWidth: number | null = null;
let unsavedDrawerWidth: number | null = null;
const drawerWidthListeners = new Set<() => void>();

/** Shows a width while the user drags a handle, before it is stored. */
function setLiveDrawerWidth(width: number | null): void {
  if (liveDrawerWidth === width) return;
  liveDrawerWidth = width;
  drawerWidthListeners.forEach((listener) => listener());
}

/** Sets the width of every right panel, kept for the next visit. */
function storeDrawerWidth(width: number): void {
  try {
    window.localStorage.setItem(DRAWER_WIDTH_STORAGE_KEY, String(width));
    unsavedDrawerWidth = null;
  } catch {
    // Without storage, the width holds until the page reloads.
    unsavedDrawerWidth = width;
  }
  liveDrawerWidth = null;
  drawerWidthListeners.forEach((listener) => listener());
}

function subscribeDrawerWidth(listener: () => void): () => void {
  drawerWidthListeners.add(listener);
  return () => {
    drawerWidthListeners.delete(listener);
  };
}

function currentDrawerWidth(): number {
  return liveDrawerWidth ?? unsavedDrawerWidth ?? getStoredDrawerWidth();
}

/**
 * The width of the right panels, live: it changes while the user drags a
 * handle, so that every panel and a layout that leaves room for them keep
 * the same width.
 */
export function useDrawerWidth(): number {
  return React.useSyncExternalStore(
    subscribeDrawerWidth,
    currentDrawerWidth,
    getStoredDrawerWidth
  );
}

/**
 * The width of the box that holds the panel of `handle`: the zone of the
 * panel, or the window for a fixed panel. It follows the box while the
 * panel is shown.
 */
function usePanelRoom(
  handle: React.RefObject<HTMLElement | null>,
  shown: boolean,
  viewport: number
): number {
  const [room, setRoom] = React.useState<number | null>(null);
  React.useLayoutEffect(() => {
    const holder = shown ? handle.current?.parentElement?.offsetParent : null;
    if (!(holder instanceof HTMLElement)) {
      setRoom(null);
      return;
    }
    const read = () => setRoom(holder.clientWidth || null);
    read();
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver(read);
    observer.observe(holder);
    return () => observer.disconnect();
  }, [handle, shown]);
  return room ?? viewport;
}

/**
 * The resize handle on the left edge of the right panel that holds it, as
 * its child. A drag, the arrow keys, Home and End set the width of every
 * right panel; the layout beside the panels follows during a drag. The panel
 * may take all of its zone but a strip that keeps the canvas in reach, and
 * beside the page it keeps the cap of a narrow window (see panelWidth).
 */
export function PanelResizeHandle({
  shown,
  modal = false,
}: {
  /** True while the panel is shown, so that the size of its zone is known. */
  shown: boolean;
  /** A modal panel covers the page: the cap beside the page does not apply. */
  modal?: boolean;
}) {
  const { ts } = useI18n();
  const handleRef = React.useRef<HTMLDivElement>(null);
  const viewport = useViewportWidth();
  const room = usePanelRoom(handleRef, shown, viewport);
  const cap = modal ? undefined : viewport;
  const maxWidth = panelMaxWidthIn(room, cap);
  const minWidth = Math.min(PANEL_MIN_WIDTH, maxWidth);
  const width = clampPanelWidth(useDrawerWidth(), room, cap);
  const clamp = (next: number) => clampPanelWidth(next, room, cap);

  const onMouseDown = (event: React.MouseEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth =
      handleRef.current?.parentElement?.getBoundingClientRect().width || width;
    // A click without a drag keeps the stored width, which the zone may
    // show narrower than it is.
    let latest: number | null = null;
    const onMove = (move: MouseEvent) => {
      latest = clamp(startWidth + startX - move.clientX);
      setLiveDrawerWidth(latest);
    };
    const onUp = () => {
      if (latest !== null) storeDrawerWidth(latest);
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  };

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    const targets: Record<string, number> = {
      ArrowLeft: width + DRAWER_WIDTH_STEP,
      ArrowRight: width - DRAWER_WIDTH_STEP,
      Home: minWidth,
      End: maxWidth,
    };
    const next = targets[event.key];
    if (next === undefined) return;
    event.preventDefault();
    storeDrawerWidth(clamp(next));
  };

  return (
    <div
      ref={handleRef}
      role="separator"
      aria-label={ts('Resize step details')}
      aria-orientation="vertical"
      aria-valuemin={minWidth}
      aria-valuemax={maxWidth}
      aria-valuenow={width}
      tabIndex={0}
      title={ts('Resize step details')}
      className="group absolute left-0 top-0 z-20 h-full w-3 -translate-x-1.5 cursor-col-resize touch-none outline-none"
      onMouseDown={onMouseDown}
      onKeyDown={onKeyDown}
    >
      <div className="mx-auto h-full w-px bg-transparent transition-colors group-hover:bg-primary/60 group-focus:bg-primary" />
    </div>
  );
}

/**
 * The panel on the right side of the page, over its full height, where the
 * details of a step show. The width is resizable and is kept between visits.
 */
export function StepSideDrawer({
  isOpen,
  onClose,
  hasContent,
  modal = true,
  header,
  labelledBy,
  label,
  panelId,
  onRestore,
  closeOnPageEscape = false,
  children,
}: StepSideDrawerProps) {
  useRightPanelClaim(panelId, isOpen, onClose, onRestore);
  const placement = usePanelPlacement();
  const viewport = useViewportWidth();
  const drawerWidth = useDrawerWidth();
  const [shouldRender, setShouldRender] = React.useState(false);
  const [isVisible, setIsVisible] = React.useState(false);
  const drawerRef = React.useRef<HTMLElement>(null);
  const previouslyFocusedRef = React.useRef<HTMLElement | null>(null);
  // The last control outside the drawer that had the focus: where the focus
  // goes back when a drawer beside the page closes under the focus.
  const lastOutsideRef = React.useRef<HTMLElement | null>(null);

  React.useEffect(() => {
    if (modal) return;
    const remember = (event: FocusEvent) => {
      const target = event.target;
      if (
        target instanceof HTMLElement &&
        target !== document.body &&
        !target.closest('[data-side-drawer]')
      ) {
        lastOutsideRef.current = target;
      }
    };
    document.addEventListener('focusin', remember);
    return () => document.removeEventListener('focusin', remember);
  }, [modal]);

  // Lifecycle (mount, focus, exit animation) keys on presence only, so live
  // updates that replace the content identity do not re-run it and steal
  // focus back to the close button.
  React.useEffect(() => {
    let closeTimer: number | undefined;
    let animationFrame: number | undefined;

    if (isOpen && hasContent) {
      if (modal) {
        previouslyFocusedRef.current =
          document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null;
      }
      setShouldRender(true);
      animationFrame = window.requestAnimationFrame(() => {
        setIsVisible(true);
        // Focus the panel itself so a pointer click does not paint a focus
        // ring on the close button; Tab still reaches it first. A drawer
        // beside the page leaves the focus where the user put it.
        if (modal) drawerRef.current?.focus();
      });
    } else {
      setIsVisible(false);
      closeTimer = window.setTimeout(() => {
        const active = document.activeElement;
        const lost =
          !active ||
          active === document.body ||
          !!drawerRef.current?.contains(active);
        setShouldRender(false);
        previouslyFocusedRef.current?.focus();
        previouslyFocusedRef.current = null;
        // A drawer beside the page: the focus fell to the page when its
        // content left. Give it back to the control that opened it.
        const opener = lastOutsideRef.current;
        if (!modal && lost && opener?.isConnected) opener.focus();
      }, 180);
    }

    return () => {
      if (animationFrame !== undefined) {
        window.cancelAnimationFrame(animationFrame);
      }
      if (closeTimer !== undefined) {
        window.clearTimeout(closeTimer);
      }
    };
  }, [isOpen, hasContent, modal]);

  React.useEffect(() => {
    if (!isOpen) {
      return;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      // A dialog or menu inside the drawer already handled the key.
      if (event.defaultPrevented) {
        return;
      }

      if (event.key === 'Escape') {
        // Beside the page, Escape belongs to the drawer only while it has focus.
        if (
          !modal &&
          !drawerRef.current?.contains(document.activeElement) &&
          !(
            closeOnPageEscape &&
            !drawerRef.current?.closest('[inert]') &&
            !keepsEscape(document.activeElement)
          )
        ) {
          return;
        }
        onClose();
        return;
      }

      if (!modal || event.key !== 'Tab') {
        return;
      }
      const focusableElements =
        drawerRef.current?.querySelectorAll<HTMLElement>(
          [
            'a[href]',
            'button:not([disabled])',
            'textarea:not([disabled])',
            'input:not([disabled])',
            'select:not([disabled])',
            '[tabindex]:not([tabindex="-1"])',
          ].join(',')
        );
      if (!focusableElements || focusableElements.length === 0) {
        event.preventDefault();
        return;
      }

      const firstElement = focusableElements.item(0);
      const lastElement = focusableElements.item(focusableElements.length - 1);
      if (!firstElement || !lastElement) {
        event.preventDefault();
        return;
      }

      // The drawer itself, focused on open, comes before its first control.
      if (
        event.shiftKey &&
        (document.activeElement === firstElement ||
          document.activeElement === drawerRef.current)
      ) {
        event.preventDefault();
        lastElement.focus();
      } else if (!event.shiftKey && document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, modal, onClose, closeOnPageEscape]);

  React.useEffect(() => {
    if (!shouldRender || !modal) {
      return;
    }
    const appRoot = document.getElementById('root');
    const previousAriaHidden = appRoot?.getAttribute('aria-hidden') ?? null;
    appRoot?.setAttribute('aria-hidden', 'true');

    return () => {
      if (!appRoot) {
        return;
      }
      if (previousAriaHidden === null) {
        appRoot.removeAttribute('aria-hidden');
      } else {
        appRoot.setAttribute('aria-hidden', previousAriaHidden);
      }
    };
  }, [shouldRender, modal]);

  if (!shouldRender) {
    return null;
  }

  const panel = (
    <aside
      ref={drawerRef}
      role={modal ? 'dialog' : 'complementary'}
      aria-modal={modal ? true : undefined}
      aria-labelledby={labelledBy}
      aria-label={labelledBy ? undefined : label}
      tabIndex={-1}
      data-testid="step-side-drawer"
      data-side-drawer=""
      data-panel-shell={modal ? undefined : 'right'}
      className={cn(
        'outline-none transition-[opacity,translate] duration-200 ease-out',
        modal &&
          'z-10 flex h-full max-w-full flex-col border-l border-border bg-card shadow-lg shadow-foreground/10',
        modal ? 'relative' : panelShellClass(placement),
        // A panel beside the page only fades: a host measures its box to
        // clear the canvas, and a slide would put the box off screen.
        isVisible
          ? 'opacity-100'
          : modal
            ? 'translate-x-full opacity-0'
            : 'opacity-0',
        modal && isVisible && 'translate-x-0'
      )}
      style={{
        // A modal drawer covers the page: the cap beside the page does not apply.
        width: modal
          ? `min(100vw, ${drawerWidth}px)`
          : panelWidth(drawerWidth, viewport),
      }}
    >
      <PanelResizeHandle shown={isOpen} modal={modal} />
      {header}
      {children}
    </aside>
  );

  // Beside the page, the panel stays in the tree of its host, so that it
  // reads the top that the host sets in --ndv-top.
  if (!modal) {
    return panel;
  }

  return createPortal(
    <div className="fixed inset-0 z-[60] flex justify-end">
      <I18nProps>
        <button
          type="button"
          tabIndex={-1}
          aria-label="Close step details"
          className={cn(
            'absolute inset-0 h-full w-full cursor-default bg-transparent transition-opacity duration-200 ease-out',
            isVisible ? 'opacity-100' : 'opacity-0'
          )}
          onClick={onClose}
        />
      </I18nProps>
      {panel}
    </div>,
    document.body
  );
}
