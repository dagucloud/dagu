// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, RefreshCw } from 'lucide-react';
import * as React from 'react';
import {
  HostEnvironment,
  WorkerAccessReason,
  type components,
} from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';
import { Spinner } from '@/components/ui/spinner';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import {
  addressProblem,
  goTrimSpace,
  coordinatorEnvironment,
  inKubernetes,
  initialAddress,
  movedEndpoint,
  podAddress,
  suggestedAddresses,
  type AddressProblem,
} from './address';
import {
  ProblemText,
  reasonText,
  requestProblem,
  Step,
  type RequestProblem,
} from './parts';
import { allReady, ReadinessBody } from './ReadinessStep';
import { WorkerStep, type CreatedInvite } from './WorkerStep';

type Worker = components['schemas']['Worker'];
type JoinReadiness = components['schemas']['WorkerJoinReadinessResponse'];

const LOCAL_NODE = 'local';

// Where workers run, which a Kubernetes coordinator cannot tell: its
// advertised address is often a Service name only pods in the cluster
// resolve.
enum Placement {
  inside = 'inside',
  outside = 'outside',
}

const PLACEMENTS: [Placement, string][] = [
  [Placement.inside, 'Inside this cluster'],
  [Placement.outside, 'Outside the cluster'],
];

type Readiness =
  | { state: 'loading' }
  | { state: 'loaded'; data: JoinReadiness }
  | { state: 'failed'; problem: RequestProblem };

interface AddWorkerProps {
  /** The workers list that System status refreshes. */
  workers: Worker[];
  /** True while that refresh fails, as during a restart of the server. */
  refreshFailed: boolean;
}

// The address a placement starts the field with: none outside the
// cluster, the pod's advertised address inside it.
function placedAddress(data: JoinReadiness, placement: Placement): string {
  return placement === Placement.inside ? (podAddress(data) ?? '') : '';
}

/** The Add worker button of the Workers section, for admins. */
export function AddWorker({ workers, refreshFailed }: AddWorkerProps) {
  const isAdmin = useIsAdmin();
  const remote = useRemoteNode() !== LOCAL_NODE;
  const [open, setOpen] = React.useState(false);
  if (!isAdmin) return null;
  return (
    <>
      {/* Invites come from the server this page is served by, never through
          a remote node, so its audit entry names the admin. */}
      {remote && (
        <span className="min-w-0 text-xs text-muted-foreground">
          <I18nText text="Switch to the local server to add a worker." />
        </span>
      )}
      <Button size="xs" disabled={remote} onClick={() => setOpen(true)}>
        <Plus />
        <I18nText text="Add worker" />
      </Button>
      {open && (
        <AddWorkerDialog
          workers={workers}
          refreshFailed={refreshFailed}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}

/**
 * The Add worker dialog. It is mounted only while open, so each opening
 * reads readiness afresh.
 */
export function AddWorkerDialog({
  workers,
  refreshFailed,
  onClose,
}: AddWorkerProps & { onClose: () => void }) {
  const client = useClient();
  const [readiness, setReadiness] = React.useState<Readiness>({
    state: 'loading',
  });
  const [checking, setChecking] = React.useState(true);
  const [address, setAddress] = React.useState<string>();
  const [placement, setPlacement] = React.useState<Placement>();
  // Kept here, so an invite stays on screen when readiness changes later.
  const [invites, setInvites] = React.useState<CreatedInvite[]>([]);
  // A pending invite request keeps the dialog open and freezes the form, so
  // its invite shows for the values it was made from.
  const [creating, setCreating] = React.useState(false);
  // The invite progress and result, outside what readiness hides.
  const [status, setStatus] = React.useState<string>();
  const touched = React.useRef(false);
  // The reading the field's address came from: the first one, or the one
  // whose endpoint the admin took. A later reading never rewrites the
  // field; it offers an endpoint that moved since.
  const [opened, setOpened] = React.useState<JoinReadiness>();
  const openedRef = React.useRef<JoinReadiness | undefined>(undefined);
  // Only the latest read may change the dialog: an earlier one that ends
  // later must not replace a newer answer.
  const latest = React.useRef(0);
  // Whether the admin left the window since the last return.
  const away = React.useRef(false);
  const hostField = React.useRef<HTMLTextAreaElement>(null);
  const checkButton = React.useRef<HTMLButtonElement>(null);
  const placementField = React.useRef<HTMLInputElement>(null);

  const load = React.useCallback(async () => {
    const id = ++latest.current;
    setChecking(true);
    let next: Readiness;
    try {
      const { data, error, response } = await client.GET(
        '/workers/join-readiness',
        { params: { query: { remoteNode: LOCAL_NODE } } }
      );
      next = data
        ? { state: 'loaded', data }
        : {
            state: 'failed',
            problem: requestProblem(
              response.status,
              error,
              'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.'
            ),
          };
    } catch {
      next = {
        state: 'failed',
        problem: requestProblem(undefined, undefined, ''),
      };
    }
    if (id !== latest.current) return;
    setReadiness(next);
    if (next.state === 'loaded') {
      const loaded = next.data;
      // The placement holds only while a pod reports.
      if (!inKubernetes(loaded)) setPlacement(undefined);
      if (!openedRef.current) {
        openedRef.current = loaded;
        setOpened(loaded);
        setAddress(initialAddress(loaded));
      }
    }
    setChecking(false);
  }, [client]);

  React.useEffect(() => {
    void load();
  }, [load]);

  // The workers refresh fails while the server restarts. Its first success
  // after that means the server is back, so the dialog reads readiness again
  // instead of polling it.
  const wasFailing = React.useRef(refreshFailed);
  React.useEffect(() => {
    if (wasFailing.current && !refreshFailed) void load();
    wasFailing.current = refreshFailed;
  }, [refreshFailed, load]);

  const data = readiness.state === 'loaded' ? readiness.data : undefined;
  const ready = data ? allReady(data.coordinators) : false;

  // A restart quicker than the workers refresh leaves no failed request,
  // and can leave a Ready reading stale, as with a new port. The admin who
  // restarted Dagu elsewhere comes back to this window, so each return
  // reads readiness again: once for its focus and visibility events, and
  // newer than any read still running.
  React.useEffect(() => {
    const leave = () => (away.current = true);
    const back = () => {
      if (document.visibilityState === 'hidden') return leave();
      if (!away.current) return;
      away.current = false;
      void load();
    };
    window.addEventListener('blur', leave);
    window.addEventListener('focus', back);
    document.addEventListener('visibilitychange', back);
    return () => {
      window.removeEventListener('blur', leave);
      window.removeEventListener('focus', back);
      document.removeEventListener('visibilitychange', back);
    };
  }, [load]);

  // A reading or a read can remove the control that has the focus, such as
  // Check again, a suggestion, or a copy button. The focus then moves to the
  // address field, or to Check again while step 2 is gone.
  const lastFocus = React.useRef<HTMLElement | null>(null);
  React.useEffect(() => {
    if (lastFocus.current && !lastFocus.current.isConnected) {
      lastFocus.current = null;
      (
        hostField.current ??
        placementField.current ??
        checkButton.current
      )?.focus();
    }
  });

  const problem = address !== undefined ? addressProblem(address) : undefined;
  const answered = !data || !inKubernetes(data) || placement;
  // A recheck, as after a restart, holds step 3 until it succeeds.
  const validAddress =
    !checking && ready && answered && address && !problem
      ? goTrimSpace(address)
      : undefined;

  let step1State = 'Checking...';
  if (!checking) step1State = ready ? 'Ready' : 'Needs change';

  return (
    <Dialog open onOpenChange={(next) => !next && !creating && onClose()}>
      <DialogContent
        className="flex max-h-[90dvh] max-w-2xl flex-col gap-5 overflow-y-auto"
        onFocusCapture={(e) => (lastFocus.current = e.target as HTMLElement)}
      >
        <DialogHeader>
          <DialogTitle>
            <I18nText text="Add worker" />
          </DialogTitle>
          <DialogDescription>
            <I18nText text="Run a worker on another machine. It joins this Dagu with an invite." />
          </DialogDescription>
        </DialogHeader>

        <Step
          number={1}
          title="Accept workers from other hosts"
          state={step1State}
          live
        >
          {readiness.state === 'failed' && (
            <ProblemText problem={readiness.problem} />
          )}
          {data && <ReadinessBody coordinators={data.coordinators} />}
          {data && data.coordinators.length === 0 && (
            <p className="text-sm">
              <I18nText
                text={reasonText(WorkerAccessReason.no_coordinator) ?? ''}
              />
            </p>
          )}
          {/* It stays while a check runs, so the focus stays on it. */}
          {!ready && readiness.state !== 'loading' && (
            <Button
              ref={checkButton}
              size="xs"
              aria-disabled={checking || undefined}
              onClick={() => {
                if (!checking) void load();
              }}
            >
              {checking ? <Spinner /> : <RefreshCw />}
              <I18nText text="Check again" />
            </Button>
          )}
          {readiness.state === 'loading' && <Spinner />}
        </Step>

        {/* Each step keeps its heading, so the steps read in order. */}
        <Step
          number={2}
          title="Address workers use to reach the coordinator"
          state={data ? undefined : 'Shown after step 1'}
        >
          {data && address !== undefined && (
            <AddressFields
              data={data}
              address={address}
              hostField={hostField}
              placementField={placementField}
              locked={creating}
              placement={placement}
              moved={opened && movedEndpoint(address, opened, data)}
              onTakeMoved={(next) => {
                touched.current = true;
                setOpened(data);
                setAddress(next);
              }}
              onPlace={(next) => {
                touched.current = false;
                setPlacement(next);
                setAddress(placedAddress(data, next));
              }}
              problem={touched.current || address ? problem : undefined}
              onChange={(next) => {
                touched.current = true;
                setAddress(next);
              }}
            />
          )}
        </Step>

        <Step
          number={3}
          title="Worker"
          state={
            validAddress || invites.length > 0
              ? undefined
              : 'Shown after steps 1 and 2'
          }
        >
          {/* One region stays mounted, so progress and completion are
              announced as its text changes, also while readiness reads. */}
          <p
            role="status"
            className="text-xs text-muted-foreground empty:sr-only"
          >
            {status && <I18nText text={status} />}
          </p>
          <WorkerStep
            address={validAddress}
            workers={workers}
            invites={invites}
            onInvite={(invite) => setInvites((prev) => [...prev, invite])}
            onCreating={(pending) => {
              setCreating(pending);
            }}
            onStatus={setStatus}
          />
        </Step>
      </DialogContent>
    </Dialog>
  );
}

function AddressFields({
  data,
  address,
  hostField,
  placementField,
  locked,
  placement,
  moved,
  onTakeMoved,
  onPlace,
  problem,
  onChange,
}: {
  data: JoinReadiness;
  address: string;
  hostField: React.Ref<HTMLTextAreaElement>;
  placementField: React.Ref<HTMLInputElement>;
  /** Freezes the address while an invite request runs. */
  locked: boolean;
  placement?: Placement;
  /** An endpoint the coordinators now report in place of the field's. */
  moved?: string;
  /**
   * Takes a reported endpoint, a suggestion or the moved one, into the
   * field, so later readings compare with the reading it came from.
   */
  onTakeMoved: (address: string) => void;
  onPlace: (placement: Placement) => void;
  problem?: AddressProblem;
  onChange: (address: string) => void;
}) {
  const id = React.useId();
  const environment = coordinatorEnvironment(data);
  const suggestions = suggestedAddresses(data);
  // A Kubernetes answer outranks a container that reports beside the pod.
  let hint: string | undefined;
  if (placement === Placement.outside) {
    hint =
      "Enter the address of the coordinator Service that workers reach: its load balancer, or for NodePort, a node's address and the assigned node port. The Service's own name works only inside the cluster.";
  } else if (placement === Placement.inside) {
    hint = podAddress(data)
      ? 'Check that worker pods can reach this address. Workers in another namespace need the namespace-qualified Service address, such as dagu-coordinator.<namespace>.svc:50055.'
      : "Enter the coordinator Service's address and port. Workers in another namespace need its namespace-qualified name, such as dagu-coordinator.<namespace>.svc:50055.";
  } else if (environment === HostEnvironment.container) {
    hint =
      'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.';
  }
  const errorId = `${id}-error`;
  const hintId = `${id}-hint`;
  const kubernetes = inKubernetes(data);
  return (
    <div className="space-y-2">
      {kubernetes && (
        <fieldset className="space-y-1.5">
          <legend className="mb-1.5 text-sm">
            <I18nText text="Where the workers run" />
          </legend>
          {PLACEMENTS.map(([value, text], i) => (
            <label key={value} className="flex items-center gap-2 text-sm">
              <input
                ref={i === 0 ? placementField : undefined}
                type="radio"
                disabled={locked}
                name={`${id}-placement`}
                checked={placement === value}
                onChange={() => onPlace(value)}
              />
              <I18nText text={text} />
            </label>
          ))}
        </fieldset>
      )}
      {hint && (
        <p id={hintId} className="text-sm text-muted-foreground">
          <I18nText text={hint} />
        </p>
      )}
      {(!kubernetes || placement) && (
        <>
          <div className="space-y-1">
            <label
              className="text-xs text-muted-foreground"
              htmlFor={`${id}-address`}
            >
              <I18nText text="Address and port" />
            </label>
            {/* A one-line text area: an input would drop a pasted line
                break that the MCP action reads, and refuses inside the
                address. */}
            <Textarea
              ref={hostField}
              id={`${id}-address`}
              rows={1}
              className="min-h-9"
              placeholder="host:port"
              required
              readOnly={locked}
              value={address}
              aria-invalid={problem ? true : undefined}
              aria-describedby={
                [hint && hintId, problem && errorId]
                  .filter(Boolean)
                  .join(' ') || undefined
              }
              onKeyDown={(e) => e.key === 'Enter' && e.preventDefault()}
              onChange={(e) => onChange(e.target.value)}
            />
          </div>
          {problem && (
            <p id={errorId} className="text-xs text-destructive">
              <I18nText text={problem.text} values={problem.values} />
            </p>
          )}
          {moved && (
            <p className="flex flex-wrap items-center gap-2 text-xs">
              <I18nText
                text="The coordinator now reports {address}."
                values={{ address: moved }}
              />
              <Button
                size="xs"
                disabled={locked}
                onClick={() => onTakeMoved(moved)}
              >
                <I18nText text="Use this address" />
              </Button>
            </p>
          )}
          {suggestions.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
              <I18nText text="Addresses of this machine:" />
              {suggestions.map((suggestion) => (
                <Button
                  key={suggestion}
                  size="xs"
                  variant="ghost"
                  className="font-mono"
                  aria-pressed={address === suggestion}
                  disabled={locked}
                  onClick={() => onTakeMoved(suggestion)}
                >
                  {suggestion}
                </Button>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}
