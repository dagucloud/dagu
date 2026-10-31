// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as DialogPrimitive from '@radix-ui/react-dialog';
import { ArrowDown, ArrowRight, SlidersHorizontal, X } from 'lucide-react';
import {
  useCallback,
  useState,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
} from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import type { CanvasDirection } from '../contracts';

export interface CanvasToolbarProps {
  direction: CanvasDirection;
  onDirectionChange?(direction: CanvasDirection): void;
  /** Shows a read-only chip. */
  readOnly?: boolean;
  /**
   * Narrow-screen form: a 'Tools' button that opens a sheet with the
   * direction buttons and `toolbarEnd`. The button takes its place in the
   * nearest positioned ancestor.
   */
  compact?: boolean;
  /** The controls of the end of the toolbar; used only with `compact`. */
  toolbarEnd?: ReactNode;
  /**
   * Controls after the direction buttons, such as the zoom buttons. The
   * full form puts them in the same row, behind a separator; the compact
   * form ignores them.
   */
  trailing?: ReactNode;
  /**
   * The toolbar floats over the canvas with no background of its own: its
   * buttons carry the chrome (the host sets it). The full form only.
   */
  floating?: boolean;
}

const LAYER_SELECTOR = '[role="dialog"], [role="menu"], [role="listbox"]';

const BUTTON_CLASS =
  'flex h-7 items-center gap-1 rounded px-1.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-50';

/** Compact toolbar at the top of the canvas. */
export function CanvasToolbar(props: CanvasToolbarProps): ReactElement {
  if (props.compact) return <CompactToolbar {...props} />;
  return <FullToolbar {...props} />;
}

function useDirections() {
  const { ts } = useI18n();
  return [
    { value: 'LR', label: ts('Left to right'), Icon: ArrowRight },
    { value: 'TB', label: ts('Top to bottom'), Icon: ArrowDown },
  ] as const;
}

function ReadOnlyChip() {
  const { ts } = useI18n();
  return (
    <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
      {ts('Read-only')}
    </span>
  );
}

function FullToolbar({
  direction,
  onDirectionChange,
  readOnly = false,
  trailing,
  floating = false,
}: CanvasToolbarProps): ReactElement {
  const { ts } = useI18n();
  const directions = useDirections();

  return (
    <div
      className={cn(
        'flex items-center gap-1',
        !floating && 'rounded-md border bg-card p-0.5 shadow-sm'
      )}
    >
      {directions.map(({ value, label, Icon }) => (
        <button
          key={value}
          type="button"
          aria-label={label}
          title={direction === value ? ts('Re-align the steps') : label}
          aria-pressed={direction === value}
          disabled={!onDirectionChange}
          onClick={() => onDirectionChange?.(value)}
          className={cn(
            BUTTON_CLASS,
            direction === value && 'bg-muted text-foreground'
          )}
        >
          <Icon className="h-3.5 w-3.5" />
        </button>
      ))}
      {trailing ? (
        <>
          <span className="mx-0.5 h-5 w-px bg-border" aria-hidden="true" />
          {trailing}
        </>
      ) : null}
      {readOnly ? <ReadOnlyChip /> : null}
    </div>
  );
}

function CompactToolbar({
  direction,
  onDirectionChange,
  readOnly = false,
  toolbarEnd,
}: CanvasToolbarProps): ReactElement {
  const { ts } = useI18n();
  const directions = useDirections();
  const [open, setOpen] = useState(false);
  // The sheet sits in the canvas, not over the whole window: it must not
  // cover the sidebar or be wider than the zone.
  const [zone, setZone] = useState<HTMLElement | null>(null);
  const findZone = useCallback((element: HTMLElement | null) => {
    setZone(
      element?.closest<HTMLElement>('[data-testid="ve-canvas"]') ??
        element?.parentElement ??
        null
    );
  }, []);

  // A toggle (direction, Workflow, YAML) changes the view under the sheet,
  // so the sheet closes. Other controls, such as Undo, keep it open.
  const closeOnToggle = (event: MouseEvent<HTMLDivElement>) => {
    const target = event.target as HTMLElement;
    if (target.closest('button[aria-pressed]')) setOpen(false);
  };

  // A control in the sheet (Issues, the document selector) opens a layer in
  // a portal: a pointer or focus there keeps the sheet open.
  const keepForLayers = (event: Event) => {
    const target = event.target as Element | null;
    if (target?.closest?.(LAYER_SELECTOR)) event.preventDefault();
  };

  // Not modal: a control in the sheet can open a popover or a dialog. The
  // Tools button is the trigger, so focus returns to it on close.
  return (
    <DialogPrimitive.Root open={open} onOpenChange={setOpen} modal={false}>
      <div
        ref={findZone}
        className="absolute left-2 top-2 z-10 flex items-center gap-1"
      >
        <DialogPrimitive.Trigger asChild>
          <button
            type="button"
            className={cn(
              BUTTON_CLASS,
              'h-9 rounded-md border bg-card px-2.5 text-foreground shadow-sm'
            )}
          >
            <SlidersHorizontal className="h-4 w-4" aria-hidden="true" />
            {ts('Tools')}
          </button>
        </DialogPrimitive.Trigger>
        {readOnly ? <ReadOnlyChip /> : null}
      </div>
      <DialogPrimitive.Portal container={zone}>
        <DialogPrimitive.Content
          data-testid="ve-tools-sheet"
          aria-describedby={undefined}
          onInteractOutside={keepForLayers}
          className="absolute inset-x-0 bottom-0 z-20 grid max-h-[75%] gap-3 overflow-y-auto rounded-t-xl border border-border bg-card p-3 shadow-lg"
        >
          <DialogPrimitive.Title className="text-sm font-semibold">
            {ts('Canvas tools')}
          </DialogPrimitive.Title>
          <div onClick={closeOnToggle} className="flex flex-col gap-3">
            <div
              role="group"
              aria-label={ts('Layout direction')}
              className="flex items-center gap-1"
            >
              {directions.map(({ value, label, Icon }) => (
                <button
                  key={value}
                  type="button"
                  title={
                    direction === value ? ts('Re-align the steps') : undefined
                  }
                  aria-pressed={direction === value}
                  disabled={!onDirectionChange}
                  onClick={() => onDirectionChange?.(value)}
                  className={cn(
                    BUTTON_CLASS,
                    'h-9 px-2.5',
                    direction === value && 'bg-muted text-foreground'
                  )}
                >
                  <Icon className="h-4 w-4" aria-hidden="true" />
                  {label}
                </button>
              ))}
            </div>
            {toolbarEnd ? (
              <div
                data-testid="ve-tools-sheet-actions"
                className="flex flex-wrap items-center gap-1"
              >
                {toolbarEnd}
              </div>
            ) : null}
          </div>
          <DialogPrimitive.Close className="absolute right-3 top-3 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <X className="h-4 w-4" aria-hidden="true" />
            <span className="sr-only">{ts('Close')}</span>
          </DialogPrimitive.Close>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
