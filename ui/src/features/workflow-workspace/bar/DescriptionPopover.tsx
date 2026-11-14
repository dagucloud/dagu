// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Info } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { Z } from '../zLayers';

const PANEL_WIDTH = 384;
const MARGIN = 8;

/**
 * The (i) button of the workflow bar. A click opens a panel with the
 * description under the button. Esc, and a pointer down or focus outside,
 * close it.
 */
export function DescriptionPopover({
  description,
  className,
}: {
  description: string;
  className?: string;
}) {
  const { ts } = useI18n();
  const id = useId();
  const [open, setOpen] = useState(false);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const rect = buttonRef.current?.getBoundingClientRect();
      if (!rect) return;
      const width = Math.min(PANEL_WIDTH, window.innerWidth - 2 * MARGIN);
      setPos({
        top: rect.bottom + 4,
        left: Math.max(
          MARGIN,
          Math.min(rect.left, window.innerWidth - width - MARGIN)
        ),
      });
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onOutside = (event: Event) => {
      const target = event.target as Node;
      if (
        panelRef.current?.contains(target) ||
        buttonRef.current?.contains(target)
      ) {
        return;
      }
      setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      // Esc closes the panel only: the canvas keeps its selection.
      event.stopPropagation();
      setOpen(false);
      buttonRef.current?.focus();
    };
    document.addEventListener('pointerdown', onOutside);
    document.addEventListener('focusin', onOutside);
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('pointerdown', onOutside);
      document.removeEventListener('focusin', onOutside);
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, [open]);

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={className}
        aria-label={ts('Show description')}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        onClick={() => setOpen((v) => !v)}
      >
        <Info className="h-3.5 w-3.5" />
      </button>
      {open &&
        createPortal(
          <div
            ref={panelRef}
            id={id}
            role="dialog"
            aria-label={ts('Description')}
            data-testid="ww-description-panel"
            className={cn(
              'fixed max-h-[50vh] overflow-y-auto whitespace-pre-line rounded-lg border border-border bg-popover px-3 py-2 text-left text-sm text-popover-foreground shadow-lg',
              Z.menu
            )}
            style={{
              ...(pos ?? { top: 0, left: 0, visibility: 'hidden' }),
              width: `min(${PANEL_WIDTH}px, calc(100vw - ${2 * MARGIN}px))`,
            }}
          >
            {description}
          </div>,
          document.body
        )}
    </>
  );
}
