// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { formatRunDuration } from '@/lib/dagRunTiming';
import dayjs from '@/lib/dayjs';
import { cn } from '@/lib/utils';
import { ScrollText, X } from 'lucide-react';
import React from 'react';
import { createPortal } from 'react-dom';
import NodeStatusChip from '../common/NodeStatusChip';
import { StepDetails } from './StepDetails';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type Step = components['schemas']['Step'];
type Node = components['schemas']['Node'];

type LogStream = 'stdout' | 'stderr';

type StepDetailsDrawerProps = {
  dagName?: string;
  isOpen: boolean;
  onClose: () => void;
  step?: Step;
  /** Runtime state of the step within the inspected run; omitted in spec-only views. */
  node?: Node;
  onViewLog?: (node: Node, stream: LogStream) => void;
  onOpenSubRun?: (node: Node, subRunIndex: number) => void;
};

const DRAWER_WIDTH_STORAGE_KEY = 'dagu.stepDetailsDrawer.width';
const DEFAULT_DRAWER_WIDTH = 560;
const MIN_DRAWER_WIDTH = 420;
const MAX_DRAWER_WIDTH = 960;

function clampDrawerWidth(value: number): number {
  const viewportMax =
    typeof window === 'undefined'
      ? MAX_DRAWER_WIDTH
      : Math.max(
          MIN_DRAWER_WIDTH,
          Math.min(MAX_DRAWER_WIDTH, window.innerWidth)
        );
  return Math.min(Math.max(value, MIN_DRAWER_WIDTH), viewportMax);
}

function getStoredDrawerWidth(): number {
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

function formatRunTimestamp(value: string | undefined): string | null {
  if (!value || value === '-') {
    return null;
  }
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.format('MMM D, HH:mm:ss') : null;
}

function StepRuntimeSection({
  node,
  onViewLog,
  onOpenSubRun,
}: {
  node: Node;
  onViewLog?: (stream: LogStream) => void;
  onOpenSubRun?: (subRunIndex: number) => void;
}) {
  const errorMessage = node.error || node.rejectionReason;
  const startedAt = formatRunTimestamp(node.startedAt);
  const finishedAt = formatRunTimestamp(node.finishedAt);
  const duration = startedAt
    ? formatRunDuration(node.startedAt, node.finishedAt)
    : null;
  const subRuns = [...(node.subRuns ?? []), ...(node.subRunsRepeated ?? [])];

  const infoRows: Array<[string, string]> = [];
  if (startedAt) infoRows.push(['Started', startedAt]);
  if (finishedAt) infoRows.push(['Finished', finishedAt]);
  if (duration && duration !== '-') infoRows.push(['Duration', duration]);
  if (node.retryCount > 0) infoRows.push(['Retries', String(node.retryCount)]);
  if (node.doneCount > 1)
    infoRows.push(['Completions', String(node.doneCount)]);
  if (node.approvedBy) {
    infoRows.push([
      'Approved by',
      node.approvedAt
        ? `${node.approvedBy} (${formatRunTimestamp(node.approvedAt) ?? node.approvedAt})`
        : node.approvedBy,
    ]);
  }
  if (node.rejectedBy) {
    infoRows.push([
      'Rejected by',
      node.rejectedAt
        ? `${node.rejectedBy} (${formatRunTimestamp(node.rejectedAt) ?? node.rejectedAt})`
        : node.rejectedBy,
    ]);
  }
  if (node.humanTaskCompletedBy) {
    infoRows.push(['Completed by', node.humanTaskCompletedBy]);
  }

  return (
    <div className="mb-5 space-y-3">
      {errorMessage && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/5 p-3">
          <div className="max-h-40 overflow-auto whitespace-pre-wrap break-words text-sm text-foreground">
            {errorMessage}
          </div>
        </div>
      )}
      {infoRows.length > 0 && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          {infoRows.map(([label, value]) => (
            <React.Fragment key={label}>
              <dt className="text-muted-foreground">
                <I18nText text={label} />
              </dt>
              <dd className="min-w-0 whitespace-normal break-words tabular-nums text-foreground">
                {value}
              </dd>
            </React.Fragment>
          ))}
        </dl>
      )}
      {onViewLog && (
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium hover:bg-muted"
            onClick={() => onViewLog('stdout')}
          >
            <ScrollText className="h-3.5 w-3.5" />
            <I18nText text={'View stdout'} />
          </button>
          <button
            type="button"
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2.5 py-1.5 text-xs font-medium hover:bg-muted"
            onClick={() => onViewLog('stderr')}
          >
            <ScrollText className="h-3.5 w-3.5" />
            <I18nText text={'View stderr'} />
          </button>
        </div>
      )}
      {subRuns.length > 0 && (
        <div className="space-y-1">
          <div className="text-xs font-medium uppercase text-muted-foreground">
            <I18nText text={'Sub-runs'} />
          </div>
          {subRuns.map((subRun, index) => (
            <div
              key={`${subRun.dagRunId}-${index}`}
              className="flex items-center justify-between gap-2 text-sm"
            >
              <span className="min-w-0 whitespace-normal break-words">
                {subRun.dagName || node.step.name}{' '}
                <code className="text-xs text-muted-foreground">
                  {subRun.dagRunId}
                </code>
              </span>
              {onOpenSubRun && (
                <button
                  type="button"
                  className="rounded-md border border-border bg-background px-2 py-0.5 text-xs font-medium hover:bg-muted"
                  onClick={() => onOpenSubRun(index)}
                >
                  <I18nText text={'Open'} />
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export function StepDetailsDrawer({
  dagName,
  isOpen,
  onClose,
  step,
  node,
  onViewLog,
  onOpenSubRun,
}: StepDetailsDrawerProps) {
  const [drawerWidth, setDrawerWidth] = React.useState(getStoredDrawerWidth);
  const [shouldRender, setShouldRender] = React.useState(false);
  const [isVisible, setIsVisible] = React.useState(false);
  const [renderedStep, setRenderedStep] = React.useState(step);
  const [renderedNode, setRenderedNode] = React.useState(node);
  const drawerWidthRef = React.useRef(drawerWidth);
  const drawerRef = React.useRef<HTMLElement>(null);
  const closeButtonRef = React.useRef<HTMLButtonElement>(null);
  const previouslyFocusedRef = React.useRef<HTMLElement | null>(null);

  React.useEffect(() => {
    drawerWidthRef.current = drawerWidth;
  }, [drawerWidth]);

  const persistDrawerWidth = React.useCallback((value: number) => {
    const width = Math.round(clampDrawerWidth(value));
    setDrawerWidth(width);
    drawerWidthRef.current = width;
    try {
      window.localStorage.setItem(DRAWER_WIDTH_STORAGE_KEY, String(width));
    } catch {
      // Width persistence is optional; resizing should still work.
    }
  }, []);

  const resizeDrawer = React.useCallback((value: number) => {
    const width = Math.round(clampDrawerWidth(value));
    setDrawerWidth(width);
    drawerWidthRef.current = width;
  }, []);

  const handleResizeMouseDown = React.useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      event.preventDefault();
      const startX = event.clientX;
      const startWidth =
        drawerRef.current?.getBoundingClientRect().width ?? drawerWidth;

      const handleMouseMove = (moveEvent: MouseEvent) => {
        resizeDrawer(startWidth + startX - moveEvent.clientX);
      };

      const handleMouseUp = () => {
        persistDrawerWidth(drawerWidthRef.current);
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
      };

      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [drawerWidth, persistDrawerWidth, resizeDrawer]
  );

  const handleResizeKeyDown = React.useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>) => {
      let nextWidth: number | undefined;

      if (event.key === 'ArrowLeft') {
        nextWidth = drawerWidth + 40;
      } else if (event.key === 'ArrowRight') {
        nextWidth = drawerWidth - 40;
      } else if (event.key === 'Home') {
        nextWidth = MIN_DRAWER_WIDTH;
      } else if (event.key === 'End') {
        nextWidth = MAX_DRAWER_WIDTH;
      }

      if (nextWidth === undefined) {
        return;
      }

      event.preventDefault();
      persistDrawerWidth(nextWidth);
    },
    [drawerWidth, persistDrawerWidth]
  );

  // Lifecycle (mount, focus, exit animation) keys on presence only, so live
  // updates that replace the step/node identity do not re-run it and steal
  // focus back to the close button. Content sync happens in the next effect.
  const hasStep = !!step;
  React.useEffect(() => {
    let closeTimer: number | undefined;
    let animationFrame: number | undefined;

    if (isOpen && hasStep) {
      previouslyFocusedRef.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      setShouldRender(true);
      animationFrame = window.requestAnimationFrame(() => {
        setIsVisible(true);
        closeButtonRef.current?.focus();
      });
    } else {
      setIsVisible(false);
      closeTimer = window.setTimeout(() => {
        setShouldRender(false);
        previouslyFocusedRef.current?.focus();
        previouslyFocusedRef.current = null;
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
  }, [isOpen, hasStep]);

  // The rendered snapshot is held across the exit animation so content does
  // not blank while the drawer slides out.
  React.useEffect(() => {
    if (isOpen && step) {
      setRenderedStep(step);
      setRenderedNode(node);
    }
  }, [isOpen, step, node]);

  React.useEffect(() => {
    if (!isOpen) {
      return;
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose();
        return;
      }

      if (event.key !== 'Tab') {
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

      if (event.shiftKey && document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
      } else if (!event.shiftKey && document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  React.useEffect(() => {
    if (!shouldRender) {
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
  }, [shouldRender]);

  if (!shouldRender || !renderedStep) {
    return null;
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
      <aside
        ref={drawerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="step-details-title"
        className={cn(
          'relative z-10 flex h-full max-w-full flex-col border-l border-border bg-card shadow-lg shadow-foreground/10 transition-all duration-200 ease-out will-change-transform',
          isVisible ? 'translate-x-0 opacity-100' : 'translate-x-full opacity-0'
        )}
        style={{ width: `min(100vw, ${drawerWidth}px)` }}
      >
        <I18nProps>
          <div
            role="separator"
            aria-label="Resize step details"
            aria-orientation="vertical"
            aria-valuemin={MIN_DRAWER_WIDTH}
            aria-valuemax={MAX_DRAWER_WIDTH}
            aria-valuenow={drawerWidth}
            tabIndex={0}
            title="Resize step details"
            className="group absolute left-0 top-0 z-20 h-full w-3 -translate-x-1.5 cursor-col-resize touch-none outline-none"
            onMouseDown={handleResizeMouseDown}
            onKeyDown={handleResizeKeyDown}
          >
            <div className="mx-auto h-full w-px bg-transparent transition-colors group-hover:bg-primary/60 group-focus:bg-primary" />
          </div>
        </I18nProps>
        <header className="flex items-start justify-between gap-4 border-b border-border bg-card px-4 py-3">
          <div className="min-w-0">
            <div className="text-xs font-medium uppercase text-muted-foreground">
              {dagName || <I18nText text={'DAG'} />}
            </div>
            <h2
              id="step-details-title"
              className="mt-1 truncate text-base font-semibold text-foreground"
            >
              {renderedStep.name}
            </h2>
            <div className="mt-1 text-xs text-muted-foreground">
              {renderedNode ? (
                <NodeStatusChip status={renderedNode.status} size="sm">
                  {renderedNode.statusLabel}
                </NodeStatusChip>
              ) : (
                <I18nText text={'Step definition'} />
              )}
            </div>
          </div>
          <I18nProps>
            <Button
              ref={closeButtonRef}
              type="button"
              variant="ghost"
              size="icon"
              onClick={onClose}
              title="Close step details"
            >
              <X className="h-4 w-4" />
            </Button>
          </I18nProps>
        </header>
        <div className="min-h-0 flex-1 overflow-auto bg-background p-5">
          {renderedNode && (
            <StepRuntimeSection
              node={renderedNode}
              onViewLog={
                onViewLog
                  ? (stream) => {
                      // The drawer and the log viewer are both modal portals;
                      // close this one before opening the other.
                      onClose();
                      onViewLog(renderedNode, stream);
                    }
                  : undefined
              }
              onOpenSubRun={
                onOpenSubRun
                  ? (subRunIndex) => {
                      onClose();
                      onOpenSubRun(renderedNode, subRunIndex);
                    }
                  : undefined
              }
            />
          )}
          <StepDetails step={renderedStep} />
        </div>
      </aside>
    </div>,
    document.body
  );
}
