// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { UserMinus } from 'lucide-react';
import React from 'react';
import {
  HostEnvironment,
  WorkerAccessReason,
  WorkerConnection,
  WorkerInviteKind,
  WorkerRemovalRuns,
  type components,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import {
  type ReasonAdvice,
  WorkerRequestError,
  describeWorkerError,
  type WorkerErrorAdvice,
  useWorkerActions,
  useWorkerInvites,
} from '../hooks/useWorkerAdmin';
import { WorkerError } from './WorkerError';
import { inviteGone, inviteState } from './WorkerInvites';

type Worker = components['schemas']['Worker'];

// Remove revokes the certificate the worker connects with, so it needs one
// that the coordinator reported. An older coordinator reports neither the
// certificate nor the removal, so its workers have no Remove.
function canRemove(worker: Worker): boolean {
  return (
    worker.connection === WorkerConnection.certificate &&
    !!worker.certificateId &&
    !worker.removing
  );
}

// A worker that reported TLS or plaintext has no certificate. One without a
// connection runs against an older coordinator, which says nothing either way.
function withoutCertificate(worker: Worker): boolean {
  return (
    worker.connection === WorkerConnection.tls ||
    worker.connection === WorkerConnection.plaintext
  );
}

// The server removes workers and stops their runs on its own coordinators
// only, never on a remote node.
function useLocalNode(): boolean {
  return useRemoteNode() === 'local';
}

/**
 * WorkerRemoveAction is the Remove action of a worker row, for admins on the
 * local node. The
 * `column` action is an icon in its own column of a wide list, with a slot on
 * every row so the columns stay aligned. The `inline` action is a text button
 * under the worker ID of a narrow list, so the ID keeps the row's width.
 */
export function WorkerRemoveAction({
  worker,
  workers,
  layout,
}: {
  worker: Worker;
  workers: Worker[];
  layout: 'column' | 'inline';
}) {
  const { ts } = useI18n();
  const isAdmin = useIsAdmin();
  const local = useLocalNode();
  // The certificate the list showed when Remove was clicked; the dialog
  // acts on it only.
  const [confirming, setConfirming] = React.useState<string | null>(null);
  if (!isAdmin || !local) return null;
  const removable = canRemove(worker);
  if (layout === 'inline' && !removable) return null;

  const label = ts('Remove {worker}', { worker: worker.id });
  return (
    <div
      className={
        layout === 'column' ? 'hidden w-7 shrink-0 @5xl:block' : '@5xl:hidden'
      }
      onClick={(event) => event.stopPropagation()}
    >
      {removable &&
        (layout === 'column' ? (
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={label}
            title={label}
            onClick={() => setConfirming(worker.certificateId ?? null)}
          >
            <UserMinus />
          </Button>
        ) : (
          <Button
            size="xs"
            variant="link"
            className="h-auto px-0 text-xs"
            aria-label={label}
            onClick={() => setConfirming(worker.certificateId ?? null)}
          >
            <I18nText text="Remove" />
          </Button>
        ))}
      {confirming && (
        <RemoveWorkerDialog
          worker={worker}
          certificateId={confirming}
          workers={workers}
          onClose={() => setConfirming(null)}
        />
      )}
    </div>
  );
}

/**
 * WorkerRemovalProgress shows a removing worker's runs left, and for admins
 * Stop runs now. The count rides on the page's refresh of the workers.
 */
export function WorkerRemovalProgress({ worker }: { worker: Worker }) {
  const { ts } = useI18n();
  const isAdmin = useIsAdmin();
  const local = useLocalNode();
  // The certificate the list showed when Stop runs now was clicked.
  const [stopping, setStopping] = React.useState<string | null>(null);
  if (worker.removing !== true) return null;
  const left = worker.runningTasks?.length ?? 0;

  return (
    <div
      className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground"
      onClick={(event) => event.stopPropagation()}
    >
      <span>
        <I18nText
          text={
            left === 1
              ? 'Removing (1 run left)'
              : 'Removing ({count} runs left)'
          }
          values={{ count: left }}
        />
      </span>
      {isAdmin && local && left > 0 && (
        <>
          <Button
            size="xs"
            variant="link"
            className="h-auto min-w-0 shrink whitespace-normal px-0 text-left text-xs"
            aria-label={ts('Stop runs now on {worker}', { worker: worker.id })}
            onClick={() => setStopping(worker.certificateId ?? null)}
          >
            <I18nText text="Stop runs now" />
          </Button>
          {stopping && (
            <StopRunsDialog
              worker={worker}
              certificateId={stopping}
              onClose={() => setStopping(null)}
            />
          )}
        </>
      )}
    </div>
  );
}

/**
 * WorkerActionsNote explains, for admins, a Remove the list does not offer:
 * on a remote node, where the server removes nothing, and for a worker that
 * reported a connection without a certificate, since it would connect again
 * at once.
 */
export function WorkerActionsNote({ workers }: { workers: Worker[] }) {
  const isAdmin = useIsAdmin();
  const local = useLocalNode();
  if (!isAdmin) return null;
  const remoteOnly =
    !local && workers.some((worker) => canRemove(worker) || worker.removing);
  const peerToken = local && workers.some(withoutCertificate);
  if (!remoteOnly && !peerToken) return null;
  return (
    <p className="px-3 py-2 text-xs text-muted-foreground">
      <I18nText
        text={
          remoteOnly
            ? 'Remove and Stop runs now work on the local server only. Switch to it to use them.'
            : 'A worker on TLS or Plaintext has no certificate and no Remove, since it would connect again at once. To stop one, stop it on its host, or change the peer token if the coordinator requires one.'
        }
      />
    </p>
  );
}

// The advice of a refused removal: the reasons only a removal has, and the
// worker that is no longer listed.
function removalAdvice(ts: (source: string) => string): {
  notFound: WorkerErrorAdvice;
  reasons: ReasonAdvice;
} {
  return {
    notFound: {
      cause: ts('This worker is no longer listed.'),
      change: ts('It may be removed already. The list shows its state.'),
    },
    reasons: {
      [WorkerAccessReason.certificate_changed]: () => ({
        cause: ts(
          'This worker no longer holds the certificate the list showed, as after it joined again.'
        ),
        change: ts(
          'The worker was not removed. The list now shows the worker as it is: check it, then remove it again if needed.'
        ),
      }),
      [WorkerAccessReason.peer_token_worker]: () => ({
        cause: ts(
          'This worker connects with the peer token, so it would connect again at once.'
        ),
        change: ts('Stop it on its host, or change the peer token.'),
      }),
    },
  };
}

/**
 * Finds the invite a worker joined with. The list's cached read may predate
 * the worker's join, so an invite it lacks is read once more before the
 * dialog says anything about it: `pending` until then, then `missing` when
 * the fresh list has no such invite, or `failed` when it cannot be read.
 */
function useInviteLookup(inviteId: string | undefined) {
  const { invites, error, mutate } = useWorkerInvites(!!inviteId);
  const [reading, setReading] = React.useState(false);
  const [readOnce, setReadOnce] = React.useState(false);
  const invite = invites?.find((candidate) => candidate.id === inviteId);
  const unknown = !!inviteId && !invite;

  const retry = React.useCallback(() => {
    setReading(true);
    void mutate().finally(() => {
      setReading(false);
      setReadOnce(true);
    });
  }, [mutate]);

  const asked = React.useRef(false);
  React.useEffect(() => {
    if (!unknown || asked.current) return;
    asked.current = true;
    retry();
  }, [unknown, retry]);

  const settled = unknown && readOnce && !reading;
  return {
    invite,
    error,
    reading: unknown && (reading || !readOnce),
    failed: settled && !!error,
    missing: settled && !error,
    retry,
  };
}

// Says that the worker joined again since the dialog opened, so the dialog
// no longer acts: another removal needs its own confirmation.
function ReplacedNotice() {
  return (
    <p className="rounded border px-3 py-2">
      <I18nText
        text={
          'This worker now connects with another certificate. Close this dialog and confirm again for it.'
        }
      />
    </p>
  );
}

function RemoveWorkerDialog({
  worker,
  certificateId,
  workers,
  onClose,
}: {
  worker: Worker;
  /** The certificate the admin confirms the removal of. */
  certificateId: string;
  workers: Worker[];
  onClose: () => void;
}) {
  const { ts } = useI18n();
  const { deleteInvite, removeWorker, rereadInvites, rereadWorkers } =
    useWorkerActions();
  const lookup = useInviteLookup(worker.inviteId);
  const [runs, setRuns] = React.useState(WorkerRemovalRuns.finish);
  const [alsoDeleteInvite, setAlsoDeleteInvite] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<unknown>(null);
  // Whether a delete of the invite went through, so a removal that fails
  // after it is not reported as no change.
  const [inviteDeleted, setInviteDeleted] = React.useState(false);
  const replaced = worker.certificateId !== certificateId;

  const tasks = worker.runningTasks ?? [];
  // The choice applies only while the worker has runs. Without them a
  // removal lets any run handed out meanwhile finish.
  const sentRuns = tasks.length > 0 ? runs : WorkerRemovalRuns.finish;
  const invite = lookup.invite;
  const reusable =
    !inviteDeleted &&
    invite?.kind === WorkerInviteKind.reusable &&
    inviteState(invite, Date.now()).state === 'active';
  const others = reusable
    ? workers
        .filter((other) => other.id !== worker.id)
        .filter((other) => other.inviteId === worker.inviteId)
        .map((other) => other.id)
    : [];
  const kubernetes = worker.environment === HostEnvironment.kubernetes;

  const submit = async () => {
    if (submitting || replaced) return;
    setSubmitting(true);
    setError(null);
    let invitesChanged = false;
    try {
      // The invite goes first, so a replacement cannot join with it between
      // the two calls.
      if (reusable && alsoDeleteInvite && invite) {
        invitesChanged = true;
        await deleteInvite(invite.id).catch((err: unknown) => {
          if (!(err instanceof WorkerRequestError && err.status === 404)) {
            throw err;
          }
        });
        setInviteDeleted(true);
      }
      await removeWorker({ id: worker.id, certificateId }, sentRuns);
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setSubmitting(false);
      rereadWorkers();
      if (invitesChanged) rereadInvites('local');
    }
  };

  return (
    <ConfirmModal
      title={ts('Remove {worker}', { worker: worker.id })}
      buttonText={ts(
        sentRuns === WorkerRemovalRuns.stop
          ? 'Stop its runs and remove'
          : 'Remove'
      )}
      visible
      dismissModal={onClose}
      onSubmit={() => void submit()}
      // The invite warning must be on screen before the removal can start.
      submitDisabled={submitting || lookup.reading || replaced}
      initialFocus="cancel"
    >
      <div className="space-y-3 text-sm">
        <p>
          <I18nText
            text={
              'Its certificate is revoked, and it stays out across restarts until it gets a new invite.'
            }
          />
        </p>

        {tasks.length === 0 ? (
          <p>
            <I18nText text="It has no running runs." />
          </p>
        ) : (
          <fieldset className="space-y-2">
            <legend className="mb-1">
              <I18nText
                text={
                  tasks.length === 1
                    ? 'It has 1 running run:'
                    : 'It has {count} running runs:'
                }
                values={{ count: tasks.length }}
              />
            </legend>
            <ul className="max-h-32 space-y-0.5 overflow-auto rounded border px-2 py-1 text-xs">
              {tasks.map((task) => (
                <li key={task.dagRunId} className="flex min-w-0 gap-2">
                  <span className="truncate font-medium">{task.dagName}</span>
                  <span className="truncate font-mono text-muted-foreground">
                    {task.dagRunId}
                  </span>
                </li>
              ))}
            </ul>
            <RunsChoice
              checked={runs === WorkerRemovalRuns.finish}
              onChange={() => setRuns(WorkerRemovalRuns.finish)}
              label="Remove after its runs finish"
              detail="It gets no new runs, and is removed when its last run ends."
            />
            <RunsChoice
              checked={runs === WorkerRemovalRuns.stop}
              onChange={() => setRuns(WorkerRemovalRuns.stop)}
              label="Stop its runs and remove"
              detail="Each run is stopped as with Stop, and the worker is removed once they end."
            />
          </fieldset>
        )}

        {kubernetes && (
          <p>
            <I18nText
              text={
                'It runs in Kubernetes: its pod keeps restarting until its Deployment is scaled down.'
              }
            />
            {reusable && (
              <>
                {' '}
                <I18nText
                  text={
                    'A replacement pod can join with the invite while it is active, so delete the invite first.'
                  }
                />
              </>
            )}
          </p>
        )}

        {reusable && (
          <div className="space-y-2 rounded border px-3 py-2">
            <p>
              <I18nText
                text={
                  'It joined with a reusable invite, which still lets new workers join, a replacement for this one included.'
                }
              />
            </p>
            {others.length > 0 && (
              <p>
                <I18nText
                  text="Other workers on this invite: {workers}"
                  values={{ workers: others.join(', ') }}
                />
              </p>
            )}
            <label className="flex cursor-pointer items-start gap-2">
              <Checkbox
                className="mt-0.5"
                checked={alsoDeleteInvite}
                onCheckedChange={(value) => setAlsoDeleteInvite(value === true)}
              />
              <span>
                <I18nText text="Also delete the invite" />
                <span className="block text-xs text-muted-foreground">
                  <I18nText
                    text={
                      'Workers that joined with it keep their certificates.'
                    }
                  />
                </span>
              </span>
            </label>
          </div>
        )}

        {lookup.failed && (
          <div className="space-y-2 rounded border px-3 py-2">
            <p>
              <I18nText
                text={
                  'The invite it joined with could not be read, so this cannot say whether the invite still lets new workers join. Remove still works.'
                }
              />
            </p>
            <WorkerError
              action={false}
              advice={describeWorkerError(
                lookup.error,
                { notFound: inviteGone(ts) },
                ts
              )}
            />
            <Button size="xs" variant="outline" onClick={lookup.retry}>
              <I18nText text="Try again" />
            </Button>
          </div>
        )}

        {lookup.missing && (
          <p>
            <I18nText
              text={
                'The invite it joined with no longer exists, so it lets no new worker join.'
              }
            />
          </p>
        )}

        {replaced && <ReplacedNotice />}

        {inviteDeleted && (
          <p role="status">
            <I18nText text="The invite was deleted." />
          </p>
        )}

        {error !== null && (
          <WorkerError
            action
            advice={describeWorkerError(error, removalAdvice(ts), ts)}
          />
        )}
      </div>
    </ConfirmModal>
  );
}

function RunsChoice({
  checked,
  onChange,
  label,
  detail,
}: {
  checked: boolean;
  onChange: () => void;
  label: string;
  detail: string;
}) {
  return (
    <label className="flex cursor-pointer items-start gap-2">
      <input
        type="radio"
        name="worker-removal-runs"
        className="mt-1"
        checked={checked}
        onChange={onChange}
      />
      <span>
        <span className="font-medium">
          <I18nText text={label} />
        </span>
        <span className="block text-xs text-muted-foreground">
          <I18nText text={detail} />
        </span>
      </span>
    </label>
  );
}

function StopRunsDialog({
  worker,
  certificateId,
  onClose,
}: {
  worker: Worker;
  /** The certificate whose runs the admin confirms to stop. */
  certificateId: string;
  onClose: () => void;
}) {
  const { ts } = useI18n();
  const { removeWorker, rereadWorkers } = useWorkerActions();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<unknown>(null);
  const count = worker.runningTasks?.length ?? 0;
  const replaced = worker.certificateId !== certificateId;

  const submit = async () => {
    if (submitting || replaced) return;
    setSubmitting(true);
    setError(null);
    try {
      await removeWorker(
        { id: worker.id, certificateId },
        WorkerRemovalRuns.stop
      );
      onClose();
    } catch (err) {
      setError(err);
    } finally {
      setSubmitting(false);
      rereadWorkers();
    }
  };

  return (
    <ConfirmModal
      title={ts('Stop the runs of {worker}', { worker: worker.id })}
      buttonText={ts('Stop runs')}
      visible
      dismissModal={onClose}
      onSubmit={() => void submit()}
      submitDisabled={submitting || replaced}
      initialFocus="cancel"
    >
      <div className="space-y-2 text-sm">
        <p>
          <I18nText
            text={
              count === 1
                ? 'Its run gets a stop request, as with Stop. The worker is removed once it ends.'
                : 'Each of its {count} runs gets a stop request, as with Stop. The worker is removed once they end.'
            }
            values={{ count }}
          />
        </p>
        {replaced && <ReplacedNotice />}
        {error !== null && (
          <WorkerError
            action
            advice={describeWorkerError(error, removalAdvice(ts), ts)}
          />
        )}
      </div>
    </ConfirmModal>
  );
}
