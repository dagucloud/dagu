// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Check, FileText, Link2, Workflow } from 'lucide-react';
import React, { useEffect } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { components, Status } from '../../../../api/v1/schema';
import { useConfig } from '../../../../contexts/ConfigContext';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { useCopyFeedback } from '@/hooks/useCopyFeedback';
import StatusChip from '@/components/ui/status-chip';
import StatusIcon from '@/components/ui/status-icon';
import AutoRetryBadge from '../common/AutoRetryBadge';
import { DAGRunActions } from '../common';
import { RunMetaLine } from '../common/RunMetaLine';
import { buildDAGPageURL, buildDAGRunPageURL } from '../../lib/dagRunUrls';
import { runStatusText } from '@/lib/status-utils';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { RefreshButton } from '@/components/ui/refresh-button';

interface DAGRunHeaderProps {
  dagRun: components['schemas']['DAGRunDetails'];
  rootDAGRun?: components['schemas']['DAGRunDetails'];
  refreshFn: () => void;
}

const DAGRunHeader: React.FC<DAGRunHeaderProps> = ({
  dagRun,
  rootDAGRun,
  refreshFn,
}) => {
  const { ts } = useI18n();
  const navigate = useNavigate();
  const remoteNode = useRemoteNode();
  // The workspace scope of this page follows every link out of it.
  const workspace = useSearchParams()[0].get('workspace') || undefined;
  const config = useConfig();
  // Announces whichever copy happened last, even while the other's feedback
  // is still showing; the announcement ends with the check mark, after 2 s.
  const [lastCopy, setLastCopy] = React.useState<'link' | 'runId' | null>(null);
  React.useEffect(() => {
    if (lastCopy === null) return;
    const timer = window.setTimeout(() => setLastCopy(null), 2000);
    return () => window.clearTimeout(timer);
  }, [lastCopy]);
  const { copied: linkCopied, copy: copyLink } = useCopyFeedback(2000, () =>
    setLastCopy('link')
  );

  // The workflow page has no address for a sub-run, so its link opens the root run.
  const isSubRun = dagRun.dagRunId !== dagRun.rootDAGRunId;
  const copyLinkLabel = ts(
    linkCopied
      ? 'Link copied'
      : isSubRun
        ? 'Copy link to the root run'
        : 'Copy link to this run'
  );
  const copyRunLink = () => {
    const basePrefix = config.basePath === '/' ? '' : (config.basePath ?? '');
    const runPath = buildDAGRunPageURL({
      rootDAGRunName: dagRun.rootDAGRunName,
      rootDAGRunId: dagRun.rootDAGRunId,
      fileName: isSubRun ? undefined : dagRun.sourceFileName,
      remoteNode,
      workspace,
    });
    void copyLink(`${window.location.origin}${basePrefix}${runPath}`);
  };

  const handleRootDAGRunClick = (e: React.MouseEvent) => {
    e.preventDefault();
    navigate(
      buildDAGRunPageURL({
        rootDAGRunName: dagRun.rootDAGRunName,
        rootDAGRunId: dagRun.rootDAGRunId,
        remoteNode,
        workspace,
      })
    );
  };

  const handleParentDAGRunClick = (e: React.MouseEvent) => {
    e.preventDefault();
    if (dagRun.parentDAGRunId) {
      navigate(
        buildDAGRunPageURL({
          rootDAGRunName: dagRun.rootDAGRunName,
          rootDAGRunId: dagRun.rootDAGRunId,
          remoteNode,
          workspace,
          subDAGRunId: dagRun.parentDAGRunId,
        })
      );
    }
  };

  const handleRefresh = React.useCallback(() => {
    refreshFn();
  }, [refreshFn]);

  // Add keyboard shortcut for refresh
  useEffect(() => {
    const handleKeyPress = (e: KeyboardEvent) => {
      // Check if user is typing in an input field
      const target = e.target as HTMLElement;
      if (
        target.tagName === 'INPUT' ||
        target.tagName === 'TEXTAREA' ||
        target.contentEditable === 'true' ||
        target.closest('.monaco-editor') ||
        target.closest('[role="textbox"]')
      ) {
        return;
      }

      // Check for 'r' key without modifiers
      if (
        e.key === 'r' &&
        !e.metaKey &&
        !e.ctrlKey &&
        !e.altKey &&
        !e.shiftKey
      ) {
        e.preventDefault();
        handleRefresh();
      }
    };

    window.addEventListener('keydown', handleKeyPress);
    return () => window.removeEventListener('keydown', handleKeyPress);
  }, [handleRefresh]);

  // The workspace selects root runs only; a sub-run has no ?run there.
  const showInWorkflowURL =
    dagRun.sourceFileName && dagRun.dagRunId === dagRun.rootDAGRunId
      ? buildDAGPageURL({
          fileName: dagRun.sourceFileName,
          remoteNode,
          workspace,
          run: dagRun.dagRunId,
        })
      : null;

  const started = dagRun.status != Status.NotStarted;
  const iconButton =
    'inline-flex flex-shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground';

  return (
    <div className="@container min-w-0">
      {/* Breadcrumb navigation */}
      <nav className="mb-2 flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground empty:hidden">
        {dagRun.rootDAGRunId !== dagRun.dagRunId && (
          <>
            <span className="font-medium">
              <I18nText text={'Root:'} />
            </span>
            <a
              href={buildDAGRunPageURL({
                rootDAGRunName: dagRun.rootDAGRunName,
                rootDAGRunId: dagRun.rootDAGRunId,
                remoteNode,
                workspace,
              })}
              onClick={handleRootDAGRunClick}
              className="text-primary hover:text-primary hover:underline transition-colors font-medium"
            >
              {dagRun.rootDAGRunName}
            </a>
            {rootDAGRun && (
              <StatusChip status={rootDAGRun.status} size="sm">
                {rootDAGRun.statusLabel || ''}
              </StatusChip>
            )}
            <span className="text-muted-foreground mx-1">/</span>
          </>
        )}

        {dagRun.parentDAGRunName &&
          dagRun.parentDAGRunId &&
          dagRun.parentDAGRunName !== dagRun.rootDAGRunName &&
          dagRun.parentDAGRunName !== dagRun.name && (
            <>
              <a
                href={buildDAGRunPageURL({
                  rootDAGRunName: dagRun.rootDAGRunName,
                  rootDAGRunId: dagRun.rootDAGRunId,
                  remoteNode,
                  workspace,
                  subDAGRunId: dagRun.parentDAGRunId,
                })}
                onClick={handleParentDAGRunClick}
                className="text-primary hover:text-primary hover:underline transition-colors font-medium"
              >
                {dagRun.parentDAGRunName}
              </a>
              <span className="text-muted-foreground mx-1">/</span>
            </>
          )}
      </nav>

      {/* One line, as in the workflow header: name, status, run facts and
          actions. It wraps when the container is narrow. */}
      <div className="mb-3 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 @3xl:flex-nowrap">
        <div className="flex min-w-0 shrink items-center gap-2">
          <h1 className="min-w-0 break-words text-2xl font-semibold tracking-[-0.01em] text-foreground sm:truncate">
            {dagRun.name}
          </h1>
          <button
            type="button"
            onClick={copyRunLink}
            className={iconButton}
            title={copyLinkLabel}
            aria-label={copyLinkLabel}
          >
            {linkCopied ? (
              <Check className="h-3.5 w-3.5 text-success" />
            ) : (
              <Link2 className="h-3.5 w-3.5" />
            )}
          </button>
          {dagRun.sourceFileName && (
            <Link
              to={buildDAGPageURL({
                fileName: dagRun.sourceFileName,
                remoteNode,
                workspace,
              })}
              className={iconButton}
              title={ts('View workflow definition')}
              aria-label={ts('View workflow definition')}
            >
              <FileText className="h-3.5 w-3.5" />
            </Link>
          )}
          {showInWorkflowURL && (
            <Link
              to={showInWorkflowURL}
              className={iconButton}
              title={ts('Show in workflow')}
              aria-label={ts('Show in workflow')}
            >
              <Workflow className="h-3.5 w-3.5" />
            </Link>
          )}
          <span className="sr-only" aria-live="polite">
            {lastCopy === 'runId' ? (
              <I18nText text={'Run ID copied to clipboard'} />
            ) : lastCopy === 'link' ? (
              <I18nText text={'Run link copied to clipboard'} />
            ) : (
              ''
            )}
          </span>
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2 @3xl:flex-1 @3xl:flex-nowrap">
          <div className="flex shrink-0 items-center gap-2">
            {started && (
              <>
                <StatusIcon status={dagRun.status} />
                <span className="text-sm font-medium">
                  {ts(runStatusText(dagRun.status))}
                </span>
              </>
            )}
            <AutoRetryBadge
              status={dagRun.status}
              count={dagRun.autoRetryCount}
              limit={dagRun.autoRetryLimit}
            />
          </div>
          <div className="flex min-w-0 items-center gap-1">
            <div className="min-w-0">
              <RunMetaLine
                dagRun={dagRun}
                compact
                keepSecondary
                onRunIdCopied={() => setLastCopy('runId')}
              />
            </div>
            {started && (
              <DAGRunActions
                dagRun={dagRun}
                name={dagRun.name}
                refresh={refreshFn}
                displayMode="full"
                hideUnavailable
                isRootLevel={dagRun.rootDAGRunId === dagRun.dagRunId}
              />
            )}
          </div>
        </div>
        {started && (
          <div className="ml-auto flex shrink-0 items-center gap-2">
            <RefreshButton onRefresh={refreshFn} />
          </div>
        )}
      </div>
    </div>
  );
};

export default DAGRunHeader;
