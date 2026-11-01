// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Workflow } from 'lucide-react';
import { Fragment } from 'react';
import { Link } from 'react-router-dom';
import { RefreshButton } from '@/components/ui/refresh-button';
import StatusIcon from '@/components/ui/status-icon';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import DAGRunActions from '@/features/dag-runs/components/common/DAGRunActions';
import { RunMetaLine } from '@/features/dag-runs/components/common/RunMetaLine';
import {
  buildDAGPageURL,
  buildDAGRunPageURL,
} from '@/features/dag-runs/lib/dagRunUrls';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { runDisplayStatus } from '@/lib/runDisplayStatus';
import type { ApiDAGRunDetails } from '../contracts';
import type { WorkspaceUrlState } from '../state/workspaceUrl';

export interface PopoutBarProps {
  fileName: string;
  /** Workflow name, shown while no run is known. */
  dagName: string;
  run: ApiDAGRunDetails | undefined;
  kind: 'step' | 'run';
  step: string | null;
  carry: WorkspaceUrlState['carry'];
  refresh(): void | Promise<unknown>;
}

/** Adds the workspace scope of the current URL to a link. */
export function withCarry(
  url: string,
  carry: WorkspaceUrlState['carry']
): string {
  const extra = new URLSearchParams();
  if (carry.workspace) extra.set('workspace', carry.workspace);
  const q = extra.toString();
  if (!q) return url;
  return `${url}${url.includes('?') ? '&' : '?'}${q}`;
}

function isSubRun(run: ApiDAGRunDetails | undefined): boolean {
  return (
    !!run?.rootDAGRunId &&
    !!run.rootDAGRunName &&
    run.rootDAGRunId !== run.dagRunId
  );
}

const crumbClass =
  'font-medium text-primary transition-colors hover:text-primary hover:underline';

/**
 * Bar of a pop-out log page. A root run gets its run actions and a link back
 * to the workspace; a sub-run gets its ancestors and no action.
 */
export function PopoutBar({
  fileName,
  dagName,
  run,
  kind,
  step,
  carry,
  refresh,
}: PopoutBarProps) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const sub = isSubRun(run);

  const crumbs: { label: string; href: string }[] = [];
  if (run && sub) {
    crumbs.push({
      label: run.rootDAGRunName,
      href: buildDAGRunPageURL({
        rootDAGRunName: run.rootDAGRunName,
        rootDAGRunId: run.rootDAGRunId,
        remoteNode,
      }),
    });
    if (
      run.parentDAGRunName &&
      run.parentDAGRunId &&
      run.parentDAGRunName !== run.rootDAGRunName &&
      run.parentDAGRunName !== run.name
    ) {
      crumbs.push({
        label: run.parentDAGRunName,
        href: buildDAGRunPageURL({
          rootDAGRunName: run.rootDAGRunName,
          rootDAGRunId: run.rootDAGRunId,
          remoteNode,
          subDAGRunId: run.parentDAGRunId,
        }),
      });
    }
  }

  const runHref = run
    ? sub
      ? buildDAGRunPageURL({
          rootDAGRunName: run.rootDAGRunName,
          rootDAGRunId: run.rootDAGRunId,
          remoteNode,
          subDAGRunId: run.dagRunId,
        })
      : buildDAGRunPageURL({
          rootDAGRunName: run.name,
          rootDAGRunId: run.dagRunId,
          fileName,
          remoteNode,
        })
    : undefined;

  const workflowHref =
    run && !sub
      ? withCarry(
          buildDAGPageURL({
            fileName,
            remoteNode,
            run: run.dagRunId,
            step: kind === 'step' ? (step ?? undefined) : undefined,
            dock: 'log',
          }),
          carry
        )
      : null;

  return (
    <div className="@container min-w-0" data-testid="ww-popout-bar">
      {crumbs.length > 0 && (
        <nav className="mb-2 flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground">
          <span className="font-medium">
            <I18nText text={'Root:'} />
          </span>
          {crumbs.map((crumb) => (
            <Fragment key={crumb.href}>
              <Link to={crumb.href} className={crumbClass}>
                {crumb.label}
              </Link>
              <span className="mx-1 text-muted-foreground" aria-hidden="true">
                /
              </span>
            </Fragment>
          ))}
        </nav>
      )}
      <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
        <div className="flex min-w-0 shrink items-baseline gap-2">
          <h1 className="min-w-0 truncate text-xl font-semibold tracking-[-0.01em] text-foreground">
            {run?.name || dagName}
          </h1>
          <span className="shrink-0 text-sm text-muted-foreground">
            {kind === 'run' ? (
              ts('Run log')
            ) : (
              <>
                {ts('Step log')}
                {step && (
                  <>
                    {': '}
                    <span className="font-mono text-foreground">{step}</span>
                  </>
                )}
              </>
            )}
          </span>
        </div>
        {run && (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <StatusIcon
              status={run.status}
              healthy={runDisplayStatus(run).healthy}
            />
            <RunMetaLine dagRun={run} compact runHref={runHref} />
            {!sub && (
              <DAGRunActions
                dagRun={run}
                name={run.name}
                refresh={() => void refresh()}
                displayMode="full"
                hideUnavailable
                isRootLevel
              />
            )}
          </div>
        )}
        <div className="ml-auto flex shrink-0 items-center gap-2">
          {run && (
            <RefreshButton
              onRefresh={async () => {
                await refresh();
              }}
            />
          )}
          {workflowHref && (
            <Link
              to={workflowHref}
              className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            >
              <Workflow className="h-3.5 w-3.5" aria-hidden="true" />
              {ts('Open in workflow')}
            </Link>
          )}
        </div>
      </div>
    </div>
  );
}
