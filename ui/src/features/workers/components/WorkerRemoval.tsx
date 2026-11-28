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
import { useClock } from '@/hooks/useClock';
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

// A worker that reported TLS, one on its own certificates included, or
// plaintext did not join with an invite, so the server has no certificate of
// it to revoke. One without a connection runs against an older coordinator,
// which says nothing either way.
function onTlsOrPlaintext(worker: Worker): boolean {
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

// The one column may not grow past the dialog, so a long worker ID wraps
// instead of pushing the buttons off a narrow screen.
const SCROLLING_DIALOG =
  'sm:max-w-[500px] max-h-[calc(100dvh-2rem)] grid-cols-[minmax(0,1fr)] grid-rows-[auto_minmax(0,1fr)_auto]';
const SCROLLING_BODY = 'min-h-0 overflow-y-auto';
const WRAPPING_HEADER = 'min-w-0 [overflow-wrap:anywhere]';

// What a confirmation acts on, as the list showed it when the admin
// clicked: the row, the certificate, and the whole list, for the workers
// on that certificate and the invite. The dialog renders its choices from
// this alone, so a refresh can lock it but never change what it offers.
interface Snapshot {
  worker: Worker;
  certificateId: string;
  workers: Worker[];
}

// An open confirmation, with its own number, so that a request that ends
// after its dialog was closed cannot close a newer one.
interface OpenAction {
  kind: ActionKind;
  snapshot: Snapshot;
  request: number;
}

const StartActionContext = React.createContext<
  ((kind: ActionKind, worker: Worker) => void) | null
>(null);

/**
 * WorkerActionsHost holds the Remove and Stop runs now confirmations of the
 * workers list it wraps. A confirmation acts on the list as it was when the
 * admin clicked, and stays open, with its outcome, until the admin closes
 * it. `workers` is the page's latest workers list, and `available` false
 * when its latest read failed or missed workers.
 */
export function WorkerActionsHost({
  workers,
  available = true,
  children,
}: {
  workers: Worker[];
  available?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = React.useState<OpenAction | null>(null);
  const requests = React.useRef(0);
  const start = React.useCallback(
    (kind: ActionKind, worker: Worker) => {
      const certificateId = worker.certificateId;
      if (!certificateId) return;
      setOpen({
        kind,
        snapshot: { worker, certificateId, workers },
        request: ++requests.current,
      });
    },
    [workers]
  );
  const close = React.useCallback(
    (request: number) =>
      setOpen((current) => (current?.request === request ? null : current)),
    []
  );

  // Whether the latest list no longer shows the worker as it was at the
  // click: another certificate or none, a removal started since, no row,
  // or a read that failed or missed workers.
  const row = open
    ? workers.find((candidate) => candidate.id === open.snapshot.worker.id)
    : undefined;
  const changed =
    !!open &&
    (!available ||
      !row ||
      row.certificateId !== open.snapshot.certificateId ||
      (open.kind === 'remove' && row.removing === true));
  return (
    <StartActionContext.Provider value={start}>
      {children}
      {open?.kind === 'remove' && (
        <RemoveWorkerDialog
          key={open.request}
          snapshot={open.snapshot}
          changed={changed}
          onClose={() => close(open.request)}
        />
      )}
      {open?.kind === 'stop' && (
        <StopRunsDialog
          key={open.request}
          snapshot={open.snapshot}
          changed={changed}
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
 * reported a TLS or plaintext connection, which did not join with an invite.
 */
export function WorkerActionsNote({ workers }: { workers: Worker[] }) {
  const isAdmin = useIsAdmin();
  const local = useLocalNode();
  if (!isAdmin) return null;
  const remoteOnly =
    !local && workers.some((worker) => canRemove(worker) || worker.removing);
  const notInvited = local && workers.some(onTlsOrPlaintext);
  if (!remoteOnly && !notInvited) return null;
  return (
    <p className="px-3 py-2 text-xs text-muted-foreground">
      <I18nText
        text={
          remoteOnly
            ? 'Remove and Stop runs now work only for the server this page belongs to. To use them on the selected node, open the Dagu UI of that node.'
            : 'Remove applies only to workers that joined with an invite. A worker on TLS or Plaintext, one on its own certificates included, has no Remove: stop it on its host, or change what the coordinator accepts from it.'
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
  // The latest read decides: after a failed read, an invite from an
  // earlier one is not shown, and the dialog offers to read again.
  const invite = error
    ? undefined
    : invites?.find((candidate) => candidate.id === inviteId);
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
    if (!unknown || error || asked.current) return;
    asked.current = true;
    retry();
  }, [unknown, error, retry]);

  return {
    invite,
    error,
    reading: unknown && (reading || (!readOnce && !error)),
    failed: unknown && !reading && !!error,
    missing: unknown && readOnce && !reading && !error,
    retry,
  };
}

/**
 * Whether a confirmation no longer acts: the list showed the worker changed
 * while the admin could still confirm. It stays locked from then on.
 */
function useChangedLock(changed: boolean, armed: boolean): boolean {
  const lockNow = changed && armed;
  const [locked, setLocked] = React.useState(lockNow);
  React.useEffect(() => {
    if (lockNow) setLocked(true);
  }, [lockNow]);
  return locked || lockNow;
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

// Says that the list showed the worker changed since the dialog opened, so
// the dialog no longer acts: it needs a new confirmation.
function ChangedNotice() {
  return (
    <p className="rounded border px-3 py-2">
      <I18nText text="This worker changed. Close and confirm again." />
    </p>
  );
}

function RemoveWorkerDialog({
  snapshot,
  changed,
  onClose,
}: {
  snapshot: Snapshot;
  /** Whether the list showed the worker changed since the dialog opened. */
  changed: boolean;
  onClose: () => void;
}) {
  const { worker, certificateId, workers } = snapshot;
  const { ts } = useI18n();
  const { deleteInvite, removeWorker, rereadInvites, rereadWorkers } =
    useWorkerActions();
  const inviteId = worker.inviteId;
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
  // What the admin submitted, shown in place of the choices until it ends.
  const [submitted, setSubmitted] =
    React.useState<SubmittedRemovalProps | null>(null);
  const locked = useChangedLock(changed, !submitting && !accepted);

  const onCertificate = certificateRuns(workers, certificateId);
  const tasks = onCertificate.runs;
  // The choice applies only to a worker that had runs. Without them a
  // removal lets any run handed out meanwhile finish.
  const sentRuns = tasks.length > 0 ? runs : WorkerRemovalRuns.finish;
  const invite = lookup.invite;
  // The invite's expiry on the shared second clock, as the invites list
  // shows it: an invite that expires while the dialog is open admits no
  // worker, also while the page's refresh is paused.
  const inviteExpired = useClock(invite?.expiresAt ? 1000 : null, (now) =>
    invite ? inviteState(invite, now).state !== 'active' : false
  );
  const reusable = invite?.kind === WorkerInviteKind.reusable && !inviteExpired;
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
    // A retry of the invite delete alone leaves the workers list as it is.
    const removing = !accepted;
    try {
      if (removing) {
        target =
          reusable && alsoDeleteInvite && invite
            ? { id: invite.id, kind: invite.kind }
            : null;
        setSubmitted({ runs: sentRuns, invite: target });
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
    if (removing) rereadWorkers();
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
      // A request under way reports here: the dialog stays until it settles.
      dismissModal={submitting ? () => undefined : onClose}
      cancelDisabled={submitting}
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
      headerClassName={WRAPPING_HEADER}
      bodyClassName={SCROLLING_BODY}
    >
      <div className="space-y-3 text-sm">
        {accepted ? (
          <AcceptedRemoval invite={inviteToDelete} />
        ) : submitting && submitted ? (
          <SubmittedRemoval {...submitted} />
        ) : (
          <>
            <p>
              <I18nText
                text={
                  'Its certificate is revoked, and it stays out across restarts until it gets a new invite.'
                }
              />
            </p>

            <CertificateWorkers runs={onCertificate} />
            {tasks.length === 0 ? (
              <p>
                <I18nText text="It has no running runs." />
              </p>
            ) : (
              <fieldset className="min-w-0 space-y-2">
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
                <RunsList runs={onCertificate} />
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
                  detail="Each run on its certificate gets a cancel request, also one the list does not show, and the worker is removed within two minutes, whether or not its runs have ended."
                />
              </fieldset>
            )}

            {kubernetes && (
              <p>
                <I18nText
                  text={
                    'It runs in Kubernetes: if a Deployment manages its pod, the pod keeps restarting until the Deployment is scaled down.'
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

            {locked && <ChangedNotice />}
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

interface SubmittedRemovalProps {
  runs: WorkerRemovalRuns;
  invite: ConfirmedInvite | null;
}

// The removal under way, as the admin submitted it.
function SubmittedRemoval({ runs, invite }: SubmittedRemovalProps) {
  const { ts } = useI18n();
  return (
    <>
      <p role="status">
        <I18nText
          text={
            runs === WorkerRemovalRuns.stop
              ? 'Stopping the runs on its certificate and removing it.'
              : 'Removing it. Its runs finish first.'
          }
        />
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

type RunningTask = NonNullable<Worker['runningTasks']>[number];

// The workers and runs on a certificate, as the list showed them. A removal
// revokes the certificate, and Stop cancels its runs, so every worker on it
// counts.
interface CertificateRuns {
  workerIds: string[];
  runs: Array<{ workerId: string; task: RunningTask }>;
}

function certificateRuns(
  workers: Worker[],
  certificateId: string
): CertificateRuns {
  const sharing = workers.filter(
    (worker) => worker.certificateId === certificateId
  );
  return {
    workerIds: sharing.map((worker) => worker.id),
    runs: sharing.flatMap((worker) =>
      (worker.runningTasks ?? []).map((task) => ({
        workerId: worker.id,
        task,
      }))
    ),
  };
}

// Names the workers that share the certificate, with runs or without.
function CertificateWorkers({ runs }: { runs: CertificateRuns }) {
  if (runs.workerIds.length < 2) return null;
  return (
    <p className="break-all">
      <I18nText
        text="Workers on this certificate: {workers}"
        values={{ workers: runs.workerIds.join(', ') }}
      />
    </p>
  );
}

// The runs on the certificate, each with its worker when several share it.
function RunsList({ runs }: { runs: CertificateRuns }) {
  const shared = runs.workerIds.length > 1;
  return (
    <ul className="max-h-32 space-y-0.5 overflow-auto rounded border px-2 py-1 text-xs">
      {runs.runs.map(({ workerId, task }) => (
        <li key={`${workerId}\n${runKey(task)}`} className="min-w-0 break-all">
          {shared && (
            <div className="font-mono text-muted-foreground">{workerId}</div>
          )}
          <div className="flex min-w-0 flex-wrap gap-x-2">
            <span className="font-medium">{task.dagName}</span>
            <span className="font-mono text-muted-foreground">
              {task.dagRunId}
            </span>
          </div>
          {underRoot(task) && (
            <div className="text-muted-foreground">
              <I18nText
                text="Root run: {dag} {run}"
                values={{
                  dag: task.rootDagRunName ?? '',
                  run: task.rootDagRunId ?? '',
                }}
              />
            </div>
          )}
        </li>
      ))}
    </ul>
  );
}

// A run is named by its root run as well: a child DAG run of one root can
// share its DAG and run ID with that of another root.
function runKey(task: RunningTask): string {
  return [
    task.rootDagRunName ?? '',
    task.rootDagRunId ?? '',
    task.dagName,
    task.dagRunId,
  ].join('\n');
}

// Whether a run is a child of another run, whose root it names.
function underRoot(task: RunningTask): boolean {
  return (
    !!task.rootDagRunId &&
    (task.rootDagRunId !== task.dagRunId ||
      task.rootDagRunName !== task.dagName)
  );
}

function StopRunsDialog({
  snapshot,
  changed,
  onClose,
}: {
  snapshot: Snapshot;
  /** Whether the list showed the worker changed since the dialog opened. */
  changed: boolean;
  onClose: () => void;
}) {
  const { worker, certificateId, workers } = snapshot;
  const { ts } = useI18n();
  const { removeWorker, rereadWorkers } = useWorkerActions();
  const [submitting, setSubmitting] = React.useState(false);
  const [error, setError] = React.useState<unknown>(null);
  const onCertificate = certificateRuns(workers, certificateId);
  const locked = useChangedLock(changed, !submitting);

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
      // A request under way reports here: the dialog stays until it settles.
      dismissModal={submitting ? () => undefined : onClose}
      cancelDisabled={submitting}
      onSubmit={() => void submit()}
      // A refused stop needs a new confirmation with the current state.
      submitDisabled={submitting || locked || error !== null}
      submitOnEnter={false}
      initialFocus="cancel"
      // A long list of runs or workers scrolls inside the dialog, so the
      // buttons stay on screen.
      contentClassName={SCROLLING_DIALOG}
      headerClassName={WRAPPING_HEADER}
      bodyClassName={SCROLLING_BODY}
    >
      <div className="space-y-2 text-sm">
        {submitting ? (
          <p role="status">
            <I18nText text="Stopping the runs on its certificate and removing it." />
          </p>
        ) : (
          <>
            <p>
              <I18nText
                text={
                  'Each run on its certificate gets a cancel request, also one the list does not show. The worker is removed within two minutes, whether or not they have ended.'
                }
              />
            </p>
            <CertificateWorkers runs={onCertificate} />
            {onCertificate.runs.length > 0 && <RunsList runs={onCertificate} />}
          </>
        )}
        {locked && <ChangedNotice />}
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
