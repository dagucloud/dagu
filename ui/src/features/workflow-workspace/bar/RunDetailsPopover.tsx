// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown } from 'lucide-react';
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import { Button } from '@/components/ui/button';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useI18n } from '@/i18n/I18nProvider';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import { cn, FOCUS_RING } from '@/lib/utils';
import type { ApiDAGRunDetails } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useDockCommands } from '../dock/DockContext';
import { Z } from '../zLayers';
import {
  ConditionsSection,
  ParamsSection,
  ReusedSection,
  RunProgressSection,
  RunStatusSection,
  TestRunNote,
} from './RunDetailsParts';

const PANEL_WIDTH = 384;
const MARGIN = 8;

/**
 * The content of the run details, in order: status, progress, test run note,
 * reused inputs, conditions, parameters. Each part shows only when it applies.
 */
export function RunDetailsContent({
  details,
  onClose,
}: {
  details: ApiDAGRunDetails;
  /** Called after an action that leaves the popover: Show log, Show run. */
  onClose?: () => void;
}) {
  const run = useWorkspaceRun();
  const dock = useDockCommands();
  const display = runDisplayStatus(details);
  // A run that never ends has no progress to show: its steps never all end.
  const showProgress = run.isActive && !display.healthy;
  return (
    <div className="min-w-0 space-y-3">
      <RunStatusSection
        details={details}
        onShowLog={() => {
          dock.setTab('log', { user: true });
          onClose?.();
        }}
      />
      {showProgress && <RunProgressSection details={details} />}
      <TestRunNote details={details} />
      <ReusedSection
        details={details}
        onShowRun={(runId) => {
          run.select(runId);
          onClose?.();
        }}
      />
      <ConditionsSection details={details} />
      {details.params && (
        <div className="min-w-0">
          <ParamsSection details={details} />
        </div>
      )}
    </div>
  );
}

/**
 * The Run details button, the last item of the run chip and of the compact
 * run identity. The slot has the size of the button, so the chip keeps its
 * width. A click opens a panel under the button with the details of the run
 * that the bar does not show. Esc and a pointer down outside close it, and the
 * focus returns to the button. The tooltip shows on keyboard focus only.
 */
export function RunDetailsButton({
  details,
  compact = false,
  className,
}: {
  details: ApiDAGRunDetails;
  /** The button of the compact identity: the chip and it are both in the DOM. */
  compact?: boolean;
  /** Classes of the slot, such as its place in the flex row of the chip. */
  className?: string;
}) {
  const { ts } = useI18n();
  const panelId = useId();
  const [open, setOpen] = useState(false);
  const [tipOpen, setTipOpen] = useState(false);
  const buttonRef = useRef<HTMLButtonElement>(null);
  // A pointer down before the focus says that the focus is not the keyboard's.
  const pointerRef = useRef(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const close = useCallback(() => setOpen(false), []);
  const prefix = compact ? 'ww-bar-compact-details' : 'ww-run-details';

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
          Math.min(rect.right - width, window.innerWidth - width - MARGIN)
        ),
      });
    };
    place();
    window.addEventListener('resize', place);
    return () => window.removeEventListener('resize', place);
  }, [open]);

  // The focus moves into the dialog, so that the keyboard reads it.
  useEffect(() => {
    if (open) panelRef.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: Event) => {
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
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown, true);
    };
  }, [open]);

  const label = ts('Run details');
  return (
    <span
      data-testid={`${prefix}-slot`}
      className={cn(
        'inline-flex size-7 shrink-0 items-center justify-center',
        className
      )}
    >
      <Tooltip open={tipOpen && !open}>
        <TooltipTrigger asChild>
          <Button
            ref={buttonRef}
            type="button"
            variant="ghost"
            size="icon-sm"
            data-testid={prefix}
            aria-label={label}
            aria-haspopup="dialog"
            aria-expanded={open}
            aria-controls={open ? panelId : undefined}
            className={cn('[&_svg]:size-4', FOCUS_RING)}
            onPointerDown={() => {
              pointerRef.current = true;
            }}
            onFocus={() => setTipOpen(!pointerRef.current)}
            onBlur={() => {
              pointerRef.current = false;
              setTipOpen(false);
            }}
            onClick={() => setOpen((value) => !value)}
          >
            <ChevronDown aria-hidden="true" />
          </Button>
        </TooltipTrigger>
        <TooltipContent side="bottom">{label}</TooltipContent>
      </Tooltip>
      {open &&
        createPortal(
          <div
            ref={panelRef}
            id={panelId}
            role="dialog"
            tabIndex={-1}
            aria-label={label}
            data-testid="ww-run-details-panel"
            className={cn(
              'fixed w-[min(24rem,calc(100vw-1rem))] max-h-[70vh] overflow-y-auto rounded-lg border border-border bg-popover p-3 text-left text-xs text-popover-foreground shadow-lg outline-none',
              Z.menu
            )}
            style={pos ?? { top: 0, left: 0, visibility: 'hidden' }}
          >
            <RunDetailsContent details={details} onClose={close} />
          </div>,
          document.body
        )}
    </span>
  );
}
