// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ChevronLeft,
  ChevronRight,
  ListTree,
  Play,
  Square,
  Workflow,
} from 'lucide-react';
import React from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import type { BackfillSlotState } from '@/api/v1/schema';
import {
  LoadErrorState,
  NotFoundState,
  PageLoading,
} from '@/components/PageStates';
import { Button } from '@/components/ui/button';
import RelativeTime from '@/components/ui/relative-time';
import Title from '@/components/ui/title';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanExecute } from '@/contexts/AuthContext';
import { BackfillProgress } from '@/features/backfills/components/BackfillProgress';
import { BackfillSlotGrid } from '@/features/backfills/components/BackfillSlotGrid';
import { BackfillStatusChip } from '@/features/backfills/components/BackfillStatusChip';
import { CancelBackfillDialog } from '@/features/backfills/components/CancelBackfillDialog';
import { ResumeBackfillDialog } from '@/features/backfills/components/ResumeBackfillDialog';
import { BackfillFailures } from '@/features/backfills/components/BackfillFailures';
import { BackfillSentence } from '@/features/backfills/components/BackfillSentence';
import {
  SLOT_PAGE_SIZE,
  useBackfill,
  useBackfillSlots,
} from '@/features/backfills/hooks/useBackfill';
import {
  backfillRunsPath,
  workflowPath,
} from '@/features/backfills/lib/backfillLinks';
import {
  type Backfill,
  canResume,
  isOpenBackfill,
  ON_FAILURE_LABEL,
  RERUN_LABEL,
} from '@/features/backfills/lib/backfillState';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { isNotFoundQueryError } from '@/lib/queryErrors';

/**
 * One backfill: where it stands in a sentence, its slots by state and one
 * by one, and the controls to cancel or resume it. It stays live while the
 * backfill is open.
 */
export default function BackfillPage() {
  const { ts } = useI18n();
  const { backfillId = '' } = useParams();
  const [searchParams] = useSearchParams();
  const appBarContext = React.useContext(AppBarContext);
  // A link from a workflow on another node names that node.
  const remoteNode =
    searchParams.get('remoteNode') ||
    appBarContext.selectedRemoteNode ||
    'local';
  const canExecute = useCanExecute();
  const { data: backfill, error, mutate } = useBackfill(backfillId, remoteNode);
  const [state, setState] = React.useState<BackfillSlotState | null>(null);
  const [page, setPage] = React.useState(0);
  const slots = useBackfillSlots(backfill, remoteNode, state, page);
  const [cancelling, setCancelling] = React.useState(false);
  const [resuming, setResuming] = React.useState(false);
  const { setTitle } = appBarContext;

  React.useEffect(() => {
    setTitle(ts('Backfill {id}', { id: backfillId }));
  }, [setTitle, ts, backfillId]);

  const selectState = (next: BackfillSlotState | null) => {
    setState(next);
    setPage(0);
  };
  const replace = (next: Backfill) => void mutate(next, { revalidate: false });
  const total = slots.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / SLOT_PAGE_SIZE));
  // Slots leave a state as the backfill goes on: a page past the end moves
  // back to the last one.
  React.useEffect(() => {
    if (slots.data && page >= pages) setPage(pages - 1);
  }, [slots.data, page, pages]);

  if (!backfill) {
    if (error && isNotFoundQueryError(error)) {
      return (
        <NotFoundState
          title={ts('Backfill not found')}
          description={backfillId}
        />
      );
    }
    if (error) {
      return (
        <LoadErrorState
          title={ts('Failed to load the backfill')}
          error={error}
          onRetry={() => void mutate()}
        />
      );
    }
    return <PageLoading />;
  }

  const open = isOpenBackfill(backfill);
  const runsLink = backfillRunsPath(backfill.id, remoteNode);
  const workflowLink = workflowPath(
    backfill.fileName ?? backfill.dag,
    remoteNode
  );

  return (
    <div className="flex h-full flex-col gap-4 overflow-hidden">
      <Title>{ts('Backfill {id}', { id: backfill.id })}</Title>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <BackfillStatusChip status={backfill.status} size="md" />
          <Link
            to={workflowLink}
            className="flex min-w-0 items-center gap-1.5 rounded-md px-1.5 py-0.5 text-sm font-medium hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <Workflow className="h-4 w-4 shrink-0 text-muted-foreground" />
            <span className="truncate">{backfill.dag}</span>
          </Link>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="ghost" size="sm" asChild>
            <Link to={runsLink}>
              <ListTree />
              {ts('View runs')}
            </Link>
          </Button>
          {canExecute && open && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setCancelling(true)}
            >
              <Square />
              {ts('Cancel backfill')}
            </Button>
          )}
          {canExecute && canResume(backfill) && (
            <Button
              variant={open ? 'outline' : 'primary'}
              size="sm"
              onClick={() => setResuming(true)}
            >
              <Play />
              {ts('Resume')}
            </Button>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-auto pr-1">
        <section className="card-obsidian space-y-3 px-4 py-3">
          <BackfillSentence backfill={backfill} remoteNode={remoteNode} />
          <BackfillProgress
            backfill={backfill}
            selected={state}
            onSelect={selectState}
          />
          <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <li>
              <I18nTemplate
                text={
                  backfill.actor
                    ? 'Submitted {time} by {actor}'
                    : 'Submitted {time}'
                }
                values={{
                  time: <RelativeTime timestamp={backfill.submittedAt} />,
                  actor: backfill.actor,
                }}
              />
            </li>
            {!!backfill.generation && (
              <li>
                {backfill.generation === 1
                  ? ts('Resumed once')
                  : ts('Resumed {count} times', { count: backfill.generation })}
              </li>
            )}
            <li>{ts(RERUN_LABEL[backfill.rerun])}</li>
            <li>
              {ts('{count} at a time', { count: backfill.maxConcurrency })}
            </li>
            <li>{ts(ON_FAILURE_LABEL[backfill.onFailure])}</li>
          </ul>
        </section>

        <BackfillFailures backfill={backfill} />

        <section className="card-obsidian">
          <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
            <span className="text-xs font-medium text-muted-foreground">
              {ts('Slots')}
              {state && ` · ${total}`}
            </span>
            {pages > 1 && (
              <div className="flex items-center gap-1 text-xs text-muted-foreground">
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={ts('Previous')}
                  disabled={page === 0}
                  onClick={() => setPage(page - 1)}
                >
                  <ChevronLeft />
                </Button>
                <span className="tabular-nums">
                  {ts('Page {page} of {pages}', { page: page + 1, pages })}
                </span>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={ts('Next')}
                  disabled={page + 1 >= pages}
                  onClick={() => setPage(page + 1)}
                >
                  <ChevronRight />
                </Button>
              </div>
            )}
          </div>
          <div className="flex flex-col gap-2 px-3 py-3">
            {slots.error && (
              <p
                role="alert"
                className="flex items-center gap-2 text-sm text-destructive"
              >
                {ts('Failed to load the slots')}
                <Button
                  variant="ghost"
                  size="xs"
                  onClick={() => void slots.mutate()}
                >
                  {ts('Retry')}
                </Button>
              </p>
            )}
            {slots.data && slots.data.slots.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {ts('No slots in this state.')}
              </p>
            ) : (
              <BackfillSlotGrid
                dagName={backfill.dag}
                remoteNode={remoteNode}
                slots={slots.data?.slots ?? []}
              />
            )}
          </div>
        </section>
      </div>

      <CancelBackfillDialog
        backfill={backfill}
        remoteNode={remoteNode}
        open={cancelling}
        onOpenChange={setCancelling}
        onCancelled={replace}
      />
      <ResumeBackfillDialog
        backfill={backfill}
        remoteNode={remoteNode}
        open={resuming}
        onOpenChange={setResuming}
        onResumed={replace}
      />
    </div>
  );
}
