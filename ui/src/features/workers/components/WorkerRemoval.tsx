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
import {
  KIND_TEXT,
  inviteState,
  listUnavailable,
  shortInviteId,
} from './WorkerInvites';

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

type ActionKind = 'remove' | 'stop';

const SCROLLING_DIALOG =
  'sm:max-w-[500px] max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto]';
const SCROLLING_BODY = 'min-h-0 overflow-y-auto';

// An open confirmation: the worker as the list showed it when the admin
// clicked, the certificate it acts on, and its own number, so that a
// request that ends after its dialog was closed cannot close a newer one.
interface OpenAction {
  kind: ActionKind;
  worker: Worker;
  certificateId: string;
  request: number;
}

const StartActionContext = React.createContext<
  ((kind: ActionKind, worker: Worker) => void) | null
>(null);

/**
 * WorkerActionsHost holds the Remove and Stop runs now confirmations of the
 * workers list it wraps. A confirmation stays open, with its outcome, until
 * the admin closes it, also when a refresh drops its row, its button, or
 * the whole list. `workers` is the page's latest workers list.
 */
export function WorkerActionsHost({
  workers,
  children,
}: {
  workers: Worker[];
  children: React.ReactNode;
}) {
  const [open, setOpen] = React.useState<OpenAction | null>(null);
  const requests = React.useRef(0);
  const start = React.useCallback((kind: ActionKind, worker: Worker) => {
    const certificateId = worker.certificateId;
    if (!certificateId) return;
    setOpen({ kind, worker, certificateId, request: ++requests.current });
  }, []);
  const close = React.useCallback(
    (request: number) =>
      setOpen((current) => (current?.request === request ? null : current)),
    []
  );

  // The row as the latest list shows it, or as it was when the admin
  // clicked once the list no longer has it.
  const worker =
    open &&
    (workers.find((candidate) => candidate.id === open.worker.id) ??
      open.worker);
  return (
    <StartActionContext.Provider value={start}>
      {children}
      {open && worker && open.kind === 'remove' && (
        <RemoveWorkerDialog
          key={open.request}
          worker={worker}
          certificateId={open.certificateId}
          workers={workers}
          onClose={() => close(open.request)}
        />
      )}
      {open && worker && open.kind === 'stop' && (
        <StopRunsDialog
          key={open.request}
          worker={worker}
          certificateId={open.certificateId}
          onClose={() => close(open.request)}
        />
      )}
    </StartActionContext.Provider>
  );
}

/**
 * WorkerRemoveAction is the Remove action of a worker row, for admins on the
 * local node, inside a WorkerActionsHost. The `column` action is an icon in
 * its own column of a wide list, with a slot on every row so the columns
 * stay aligned. The `inline` action is a text button under the worker ID of
 * a narrow list, so the ID keeps the row's width.
 */
export function WorkerRemoveAction({
  worker,
  layout,
}: {
  worker: Worker;
  layout: 'column' | 'inline';
}) {
  const { ts } = useI18n();
  const isAdmin = useIsAdmin();
  const local = useLocalNode();
  const start = React.useContext(StartActionContext);
  if (!isAdmin || !local || !start) return null;
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
            onClick={() => start('remove', worker)}
          >
            <UserMinus />
          </Button>
        ) : (
          <Button
            size="xs"
            variant="link"
            className="h-auto px-0 text-xs"
            aria-label={label}
            onClick={() => start('remove', worker)}
          >
            <I18nText text="Remove" />
          </Button>
        ))}
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
  const start = React.useContext(StartActionContext);
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
      {isAdmin && local && start && left > 0 && (
        <Button
          size="xs"
          variant="link"
          className="h-auto min-w-0 shrink whitespace-normal px-0 text-left text-xs"
          aria-label={ts('Stop runs now on {worker}', { worker: worker.id })}
          onClick={() => start('stop', worker)}
        >
          <I18nText text="Stop runs now" />
        </Button>
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

// The advice of a refused removal: the reasons whose shared text speaks of
// invites, and the worker that is no longer listed.
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
      [WorkerAccessReason.own_certificates]: () => ({
        cause: ts(
          'The coordinator uses its own certificates, so it has no worker certificates to remove.'
        ),
        change: ts('Stop the worker on its host, or change the peer token.'),
      }),
      [WorkerAccessReason.older_coordinator]: () => ({
        cause: ts(
          'The coordinator runs an earlier Dagu release that cannot remove workers.'
        ),
        change: ts('Upgrade the coordinator.'),
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

/**
 * Whether a confirmation opened for `certificateId` no longer acts: the row
 * showed another certificate, or none, at any time since it opened. It
 * stays locked when the row later goes away and the page falls back to the
 * row as it was when the admin clicked.
 */
function useLockedConfirmation(worker: Worker, certificateId: string): boolean {
  const replaced = worker.certificateId !== certificateId;
  const [locked, setLocked] = React.useState(replaced);
  React.useEffect(() => {
    if (replaced) setLocked(true);
  }, [replaced]);
  return replaced || locked;
}

// Says how to try a refused request again: with a new confirmation, from
// the current state of the list.
function RefusedNotice() {
  return (
    <p className="text-muted-foreground">
      <I18nText
        text={
          'To try again, close this dialog and confirm again from the list.'
        }
      />
    </p>
  );
}

// Says that the worker joined again since the dialog opened, so the dialog
// no longer acts: another removal needs its own confirmation.
function ReplacedNotice() {
  return (
    <p className="rounded border px-3 py-2">
      <I18nText
        text={
          'This worker no longer connects with the certificate shown when this dialog opened. Close this dialog and confirm again for it.'
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
  // The invite of the row the admin confirmed; a later row does not change
  // what this dialog looks up or deletes.
  const [inviteId] = React.useState(worker.inviteId);
  // No lookup once the server accepted the removal: the retry has its
  // target already.
  const [lookupDone, setLookupDone] = React.useState(false);
  const lookup = useInviteLookup(lookupDone ? undefined : inviteId);
  const [runs, setRuns] = React.useState(WorkerRemovalRuns.finish);
  const [alsoDeleteInvite, setAlsoDeleteInvite] = React.useState(false);
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<unknown>(null);
  // The removal goes first, and the invite is deleted only once the server
  // accepted it. Each step reports its own outcome.
  const [accepted, setAccepted] = React.useState(false);
  // The invite the admin chose to delete when confirming. Once the removal
  // is accepted, the dialog shows and deletes this one only, whatever the
  // rows show by then.
  const [inviteToDelete, setInviteToDelete] =
    React.useState<ConfirmedInvite | null>(null);
  const [inviteError, setInviteError] = React.useState<unknown>(null);
  const locked = useLockedConfirmation(worker, certificateId);

  const tasks = worker.runningTasks ?? [];
  // The choice applies only while the worker has runs. Without them a
  // removal lets any run handed out meanwhile finish.
  const sentRuns = tasks.length > 0 ? runs : WorkerRemovalRuns.finish;
  const invite = lookup.invite;
  const reusable =
    invite?.kind === WorkerInviteKind.reusable &&
    inviteState(invite, Date.now()).state === 'active';
  const others = reusable
    ? workers
        .filter((other) => other.id !== worker.id)
        .filter((other) => other.inviteId === inviteId)
        .map((other) => other.id)
    : [];
  const kubernetes = worker.environment === HostEnvironment.kubernetes;

  const submit = async () => {
    if (submitting || (locked && !accepted)) return;
    setSubmitting(true);
    setError(null);
    setInviteError(null);
    let target = inviteToDelete;
    try {
      if (!accepted) {
        target =
          reusable && alsoDeleteInvite && invite
            ? { id: invite.id, kind: invite.kind }
            : null;
        await removeWorker({ id: worker.id, certificateId }, sentRuns);
        setInviteToDelete(target);
        setAccepted(true);
        setLookupDone(true);
      }
    } catch (err) {
      setError(err);
      setSubmitting(false);
      rereadWorkers();
      return;
    }
    rereadWorkers();
    if (target) {
      try {
        await deleteInvite(target.id);
      } catch (err) {
        // An invite that is gone already needs no delete.
        if (!(err instanceof WorkerRequestError && err.status === 404)) {
          setInviteError(err);
          setSubmitting(false);
          rereadInvites('local');
          return;
        }
      }
      rereadInvites('local');
    }
    setSubmitting(false);
    onClose();
  };

  return (
    <ConfirmModal
      title={ts('Remove {worker}', { worker: worker.id })}
      buttonText={ts(
        accepted
          ? 'Delete the invite'
          : sentRuns === WorkerRemovalRuns.stop
            ? 'Stop its runs and remove'
            : 'Remove'
      )}
      visible
      dismissModal={onClose}
      onSubmit={() => void submit()}
      // The invite warning must be on screen before the removal can start.
      // A refused removal needs a new confirmation with the current state.
      submitDisabled={
        submitting ||
        (!accepted && (lookup.reading || locked || error !== null))
      }
      submitOnEnter={false}
      initialFocus="cancel"
      // A long list of runs or workers scrolls inside the dialog, so the
      // buttons stay on screen.
      contentClassName={SCROLLING_DIALOG}
      bodyClassName={SCROLLING_BODY}
    >
      <div className="space-y-3 text-sm">
        {accepted ? (
          <AcceptedRemoval invite={inviteToDelete} />
        ) : (
          <>
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
                      <span className="truncate font-medium">
                        {task.dagName}
                      </span>
                      <span className="truncate font-mono text-muted-foreground">
                        {task.dagRunId}
                      </span>
                    </li>
                  ))}
                </ul>
                <RunsChoice
                  checked={runs === WorkerRemovalRuns.finish}
                  disabled={submitting}
                  onChange={() => setRuns(WorkerRemovalRuns.finish)}
                  label="Remove after its runs finish"
                  detail="It gets no new runs, and is removed when its last run ends."
                />
                <RunsChoice
                  checked={runs === WorkerRemovalRuns.stop}
                  disabled={submitting}
                  onChange={() => setRuns(WorkerRemovalRuns.stop)}
                  label="Stop its runs and remove"
                  detail="Each run gets a cancel request, and the worker is removed within two minutes, whether or not its runs have ended."
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
                        'A replacement pod can join with the invite while it is active, so delete the invite as well.'
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
                    // The submitted choice holds until its request ends.
                    disabled={submitting}
                    onCheckedChange={(value) =>
                      setAlsoDeleteInvite(value === true)
                    }
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
                    { notFound: listUnavailable(ts) },
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

            {locked && <ReplacedNotice />}
          </>
        )}

        {error !== null && (
          <WorkerError
            action
            advice={describeWorkerError(error, removalAdvice(ts), ts)}
          />
        )}
        {error !== null && <RefusedNotice />}

        {inviteError !== null && (
          <WorkerError
            action
            title={ts('The invite was not deleted.')}
            advice={describeWorkerError(
              inviteError,
              { notFound: removalAdvice(ts).notFound },
              ts
            )}
          />
        )}
      </div>
    </ConfirmModal>
  );
}

// The invite a removal deletes once the server accepted it.
interface ConfirmedInvite {
  id: string;
  kind: WorkerInviteKind;
}

// What an accepted removal still has to do, from what the admin confirmed
// only: the rows may show another worker by now.
function AcceptedRemoval({ invite }: { invite: ConfirmedInvite | null }) {
  const { ts } = useI18n();
  return (
    <>
      <p role="status">
        <I18nText text="The server accepted the removal." />
      </p>
      {invite && (
        <p>
          <I18nText
            text="Invite to delete: {id} ({kind})"
            values={{
              id: shortInviteId(invite.id),
              kind: ts(KIND_TEXT[invite.kind]),
            }}
          />
        </p>
      )}
    </>
  );
}

function RunsChoice({
  checked,
  disabled,
  onChange,
  label,
  detail,
}: {
  checked: boolean;
  disabled: boolean;
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
        disabled={disabled}
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
  const locked = useLockedConfirmation(worker, certificateId);

  const submit = async () => {
    if (submitting || locked) return;
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
      // A refused stop needs a new confirmation with the current state.
      submitDisabled={submitting || locked || error !== null}
      submitOnEnter={false}
      initialFocus="cancel"
      // A long list of runs or workers scrolls inside the dialog, so the
      // buttons stay on screen.
      contentClassName={SCROLLING_DIALOG}
      bodyClassName={SCROLLING_BODY}
    >
      <div className="space-y-2 text-sm">
        <p>
          <I18nText
            text={
              count === 1
                ? 'Its run gets a cancel request. The worker is removed within two minutes, whether or not the run has ended.'
                : 'Each of its {count} runs gets a cancel request. The worker is removed within two minutes, whether or not they have ended.'
            }
            values={{ count }}
          />
        </p>
        {locked && <ReplacedNotice />}
        {error !== null && (
          <WorkerError
            action
            advice={describeWorkerError(error, removalAdvice(ts), ts)}
          />
        )}
        {error !== null && <RefusedNotice />}
      </div>
    </ConfirmModal>
  );
}
