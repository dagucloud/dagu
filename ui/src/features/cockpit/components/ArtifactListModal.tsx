// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useContext, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { AlertCircle, Archive, Loader2, X } from 'lucide-react';
import { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import {
  type DAGRunDetailsRequestTarget,
  matchesRequestedDAGRunDetails,
} from '@/features/dag-runs/hooks/dagRunDetailsRequest';
import ArtifactsTab from '@/features/dags/components/artifacts/ArtifactsTab';
import { cn } from '@/lib/utils';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

type DAGRunSummary = components['schemas']['DAGRunSummary'];
const CLOSE_ANIMATION_MS = 200;

interface Props {
  run: DAGRunSummary | null;
  isOpen: boolean;
  onClose: () => void;
}

export function ArtifactListModal({
  run,
  isOpen,
  onClose,
}: Props): React.ReactElement | null {
  const appBarContext = useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const [shouldRender, setShouldRender] = useState(isOpen);
  const [isVisible, setIsVisible] = useState(false);
  const [renderedRun, setRenderedRun] = useState<DAGRunSummary | null>(run);
  const drawerRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  const visibleRun = isOpen ? run : renderedRun;

  useEffect(() => {
    let closeTimer: number | undefined;
    let animationFrame: number | undefined;

    if (isOpen && run) {
      previouslyFocusedRef.current =
        document.activeElement instanceof HTMLElement
          ? document.activeElement
          : null;
      setRenderedRun(run);
      setShouldRender(true);
      animationFrame = window.requestAnimationFrame(() => {
        setIsVisible(true);
        closeButtonRef.current?.focus();
      });
    } else {
      setIsVisible(false);
      closeTimer = window.setTimeout(() => {
        setShouldRender(false);
        setRenderedRun(null);
        previouslyFocusedRef.current?.focus();
        previouslyFocusedRef.current = null;
      }, CLOSE_ANIMATION_MS);
    }

    return () => {
      if (animationFrame !== undefined) {
        window.cancelAnimationFrame(animationFrame);
      }
      if (closeTimer !== undefined) {
        window.clearTimeout(closeTimer);
      }
    };
  }, [isOpen, run]);

  useEffect(() => {
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

  useEffect(() => {
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

  const target = useMemo<DAGRunDetailsRequestTarget | null>(() => {
    if (!isOpen || !run) {
      return null;
    }

    return {
      remoteNode,
      name: run.name,
      dagRunId: run.dagRunId,
    };
  }, [isOpen, remoteNode, run]);

  const {
    data: details,
    error,
    isLoading,
    isValidating,
  } = useBoundedDAGRunDetails({
    target,
    enabled: target !== null,
    pollIntervalMs: isOpen ? 2000 : 0,
  });

  const displayDetails = matchesRequestedDAGRunDetails(
    details,
    visibleRun?.dagRunId ?? ''
  )
    ? details
    : null;

  if (!shouldRender || !visibleRun) {
    return null;
  }

  return createPortal(
    <div className="fixed inset-0 z-[60] flex justify-end">
      <I18nProps><button
        type="button"
        tabIndex={-1}
        aria-label="Close artifact preview"
        className={cn(
          'absolute inset-0 h-full w-full cursor-default bg-black/20 transition-opacity duration-200 ease-out',
          isVisible ? 'opacity-100' : 'opacity-0'
        )}
        onClick={onClose}
      /></I18nProps>
      <aside
        ref={drawerRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="cockpit-artifacts-title"
        className={cn(
          'relative z-10 flex h-full w-full flex-col border-l border-border bg-background shadow-xl transition-all duration-200 ease-out will-change-transform sm:w-[calc(100vw-2rem)]',
          isVisible ? 'translate-x-0 opacity-100' : 'translate-x-full opacity-0'
        )}
      >
        <header className="flex shrink-0 items-start justify-between gap-4 border-b border-border px-5 py-4">
          <div className="min-w-0">
            <h2
              id="cockpit-artifacts-title"
              className="flex items-center gap-2 text-lg font-semibold leading-none tracking-tight"
            >
              <Archive className="h-5 w-5 text-primary" />
              <I18nText text={"Artifacts"} />
              {isValidating && (
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
              )}
            </h2>
            <p className="mt-1 truncate text-sm text-muted-foreground">
              {visibleRun.name} / {visibleRun.dagRunId}
            </p>
          </div>
          <I18nProps><Button
            ref={closeButtonRef}
            type="button"
            variant="ghost"
            size="icon"
            onClick={onClose}
            title="Close artifact preview"
          >
            <X className="h-4 w-4" />
          </Button></I18nProps>
        </header>

        <div className="min-h-0 flex-1 overflow-hidden px-5 py-4">
          {isLoading && !displayDetails ? (
            <div className="flex h-full min-h-80 items-center justify-center">
              <LoadingIndicator />
            </div>
          ) : error && !displayDetails ? (
            <div className="flex items-start gap-2 rounded-md bg-destructive/5 px-3 py-3 text-sm text-destructive">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error.message || <I18nText text={"Failed to load DAG run details"} />}</span>
            </div>
          ) : displayDetails ? (
            <ArtifactsTab
              dagRun={displayDetails}
              artifactEnabled={visibleRun.artifactsAvailable ?? false}
              className="h-full"
              fillHeight
            />
          ) : null}
        </div>
      </aside>
    </div>,
    document.body
  );
}
