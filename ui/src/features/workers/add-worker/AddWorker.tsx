// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RefreshCw } from 'lucide-react';
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
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import {
  addressProblem,
  goTrimSpace,
  coordinatorEnvironment,
  inKubernetes,
  initialAddress,
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
  /** True while that refresh fails, as during a restart of the server. */
  refreshFailed: boolean;
}

// The address an untouched field starts with: none outside the cluster,
// the pod's advertised address inside it, else the usual default.
function defaultAddress(data: JoinReadiness, placement?: Placement): string {
  if (placement === Placement.outside) return '';
  if (placement === Placement.inside) return podAddress(data) ?? '';
  return initialAddress(data);
}

/**
 * The Add worker dialog. It is mounted only while open, so each opening
 * reads readiness afresh.
 */
export function AddWorkerDialog({
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
  // The placement as load reads it, to reset an untouched field.
  const placed = React.useRef<Placement | undefined>(undefined);
  const touched = React.useRef(false);
  // Only the latest read may change the dialog: an earlier one that ends
  // later must not replace a newer answer.
  const latest = React.useRef(0);
  const reading = React.useRef(false);
  const hostField = React.useRef<HTMLTextAreaElement>(null);
  const checkButton = React.useRef<HTMLButtonElement>(null);
  const placementField = React.useRef<HTMLInputElement>(null);

  const load = React.useCallback(async () => {
    const id = ++latest.current;
    reading.current = true;
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
    // The placement holds only while a pod reports.
    if (next.state === 'loaded' && !inKubernetes(next.data)) {
      placed.current = undefined;
      setPlacement(undefined);
    }
    // A new reading resets the defaults the admin has not changed, as a
    // port that changed in the restart.
    if (next.state === 'loaded' && !touched.current) {
      setAddress(defaultAddress(next.data, placed.current));
    }
    reading.current = false;
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
  // restarted Dagu elsewhere comes back to this window, so its return reads
  // readiness again, once while no read runs.
  React.useEffect(() => {
    const reload = () => {
      if (document.visibilityState !== 'hidden' && !reading.current) {
        void load();
      }
    };
    window.addEventListener('focus', reload);
    document.addEventListener('visibilitychange', reload);
    return () => {
      window.removeEventListener('focus', reload);
      document.removeEventListener('visibilitychange', reload);
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
    <Dialog open onOpenChange={(next) => !next && onClose()}>
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
              placement={placement}
              onPlace={(next) => {
                placed.current = next;
                touched.current = false;
                setPlacement(next);
                setAddress(defaultAddress(data, next));
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
          state={validAddress ? undefined : 'Shown after steps 1 and 2'}
        />
      </DialogContent>
    </Dialog>
  );
}

function AddressFields({
  data,
  address,
  hostField,
  placementField,
  placement,
  onPlace,
  problem,
  onChange,
}: {
  data: JoinReadiness;
  address: string;
  hostField: React.Ref<HTMLTextAreaElement>;
  placementField: React.Ref<HTMLInputElement>;
  placement?: Placement;
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
                  onClick={() => onChange(suggestion)}
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
