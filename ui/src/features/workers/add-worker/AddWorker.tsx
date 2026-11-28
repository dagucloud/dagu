// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Plus, RefreshCw } from 'lucide-react';
import * as React from 'react';
import { HostEnvironment, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Spinner } from '@/components/ui/spinner';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import {
  addressProblem,
  initialAddress,
  joinAddress,
  suggestedHosts,
  type WorkerAddress,
} from './address';
import {
  ProblemText,
  requestProblem,
  Step,
  type RequestProblem,
} from './parts';
import { allReady, ReadinessBody } from './ReadinessStep';

type JoinReadiness = components['schemas']['WorkerJoinReadinessResponse'];

type Readiness =
  | { state: 'loading' }
  | { state: 'loaded'; data: JoinReadiness }
  | { state: 'failed'; problem: RequestProblem };

interface AddWorkerProps {
  /** True while that refresh fails, as during a restart of the server. */
  refreshFailed: boolean;
}

/** The Add worker button of the Workers section, for admins. */
export function AddWorker({ refreshFailed }: AddWorkerProps) {
  const isAdmin = useIsAdmin();
  const [open, setOpen] = React.useState(false);
  if (!isAdmin) return null;
  return (
    <>
      <Button size="xs" onClick={() => setOpen(true)}>
        <Plus />
        <I18nText text="Add worker" />
      </Button>
      {/* Mounted only while open, so each opening reads readiness afresh. */}
      {open && (
        <AddWorkerDialog
          refreshFailed={refreshFailed}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}

function AddWorkerDialog({
  refreshFailed,
  onClose,
}: AddWorkerProps & { onClose: () => void }) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const [readiness, setReadiness] = React.useState<Readiness>({
    state: 'loading',
  });
  const [checking, setChecking] = React.useState(true);
  const [address, setAddress] = React.useState<WorkerAddress>();
  const [touched, setTouched] = React.useState(false);

  const load = React.useCallback(async () => {
    setChecking(true);
    try {
      const { data, error, response } = await client.GET(
        '/workers/join-readiness',
        { params: { query: { remoteNode } } }
      );
      if (data) {
        setReadiness({ state: 'loaded', data });
        // A later reading keeps what the admin typed.
        setAddress((prev) => prev ?? initialAddress(data));
      } else {
        setReadiness({
          state: 'failed',
          problem: requestProblem(
            response.status,
            error?.message,
            'The coordinator is from an earlier Dagu release. Upgrade every coordinator to add workers.'
          ),
        });
      }
    } catch {
      setReadiness({
        state: 'failed',
        problem: { text: 'The server did not answer.' },
      });
    } finally {
      setChecking(false);
    }
  }, [client, remoteNode]);

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
  const problem = address ? addressProblem(address) : undefined;
  const validAddress =
    ready && address && !problem ? joinAddress(address) : undefined;

  let step1State = 'Checking...';
  if (!checking) step1State = ready ? 'Ready' : 'Needs change';

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="flex max-h-[90dvh] max-w-2xl flex-col gap-5 overflow-y-auto">
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
        >
          {readiness.state === 'failed' && (
            <ProblemText problem={readiness.problem} />
          )}
          {data && <ReadinessBody coordinators={data.coordinators} />}
          {data && data.coordinators.length === 0 && (
            <p className="text-sm">
              <I18nText text="No coordinator is running. Start one, such as with dagu start-all, then check again." />
            </p>
          )}
          {!checking && !ready && (
            <Button size="xs" onClick={() => void load()}>
              <RefreshCw />
              <I18nText text="Check again" />
            </Button>
          )}
          {checking && readiness.state === 'loading' && <Spinner />}
        </Step>

        {data && address && (
          <Step number={2} title="Address workers use to reach the coordinator">
            <AddressFields
              data={data}
              address={address}
              problem={touched || address.host ? problem : undefined}
              onChange={(next) => {
                setTouched(true);
                setAddress(next);
              }}
            />
          </Step>
        )}

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
  problem,
  onChange,
}: {
  data: JoinReadiness;
  address: WorkerAddress;
  problem?: ReturnType<typeof addressProblem>;
  onChange: (address: WorkerAddress) => void;
}) {
  const { ts } = useI18n();
  const environment = data.coordinators.find((c) => c.report)?.report
    ?.environment;
  const hosts = suggestedHosts(data);
  let hint: string | undefined;
  if (environment === HostEnvironment.container) {
    hint =
      'Enter the address of the machine that runs the container. Dagu cannot see it from inside the container.';
  } else if (environment === HostEnvironment.kubernetes) {
    hint =
      'Enter the address of the coordinator Service: its load balancer, or a node for NodePort.';
  }
  return (
    <div className="space-y-2">
      {hint && (
        <p className="text-sm text-muted-foreground">
          <I18nText text={hint} />
        </p>
      )}
      <div className="flex gap-2">
        <Input
          aria-label={ts('Address')}
          placeholder={ts('IP address or DNS name')}
          className="flex-1"
          required
          value={address.host}
          aria-invalid={!!problem || undefined}
          onChange={(e) => onChange({ ...address, host: e.target.value })}
        />
        <Input
          aria-label={ts('Port')}
          inputMode="numeric"
          className="w-24"
          value={address.port}
          onChange={(e) => onChange({ ...address, port: e.target.value })}
        />
      </div>
      {problem && (
        <p className="text-xs text-destructive">
          <I18nText text={problem.text} values={problem.values} />
        </p>
      )}
      {hosts.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          <I18nText text="Addresses of this machine:" />
          {hosts.map((host) => (
            <Button
              key={host}
              size="xs"
              variant="ghost"
              className="font-mono"
              aria-pressed={address.host === host}
              onClick={() => onChange({ ...address, host })}
            >
              {host}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}
