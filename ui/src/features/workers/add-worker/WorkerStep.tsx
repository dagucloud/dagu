// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RefreshCw } from 'lucide-react';
import * as React from 'react';
import { WorkerInviteKind, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Textarea } from '@/components/ui/textarea';
import { Spinner } from '@/components/ui/spinner';
import { TabStrip, tabStripTabId } from '@/components/ui/tab-strip';
import { useConfig } from '@/contexts/ConfigContext';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import {
  checkCommand,
  normalizeLabels,
  publishedImage,
  workerCommand,
  WorkerTab,
  type CommandInput,
} from './commands';
import {
  CommandBlock,
  ProblemText,
  requestProblem,
  type RequestProblem,
} from './parts';

type Worker = components['schemas']['Worker'];

/** How long the dialog waits before it lists the usual causes. */
export const ARRIVAL_TIMEOUT_MS = 60_000;

/** An invite this dialog created, with the address it was made for. */
export interface CreatedInvite {
  id: string;
  invite: string;
  expiresAt?: string;
  createdAt: number;
  address: string;
}

/** The invites of one opening of the dialog, one per kind. */
export type CreatedInvites = Partial<Record<WorkerInviteKind, CreatedInvite>>;

const LOCAL_NODE = 'local';
const TAB_PREFIX = 'add-worker-tab';
const INSTALL_DOCS_URL = 'https://docs.dagu.sh/getting-started/installation/';

// Where each tab's command runs.
const RUN_WHERE: Record<WorkerTab, string> = {
  [WorkerTab.linux]: 'Install Dagu on the worker host, then run this there.',
  [WorkerTab.docker]:
    'Run this on the worker host, which needs Docker. For another worker on the same host, change the volume dagu-worker to one of its own.',
  [WorkerTab.kubernetes]:
    'Save this as a file and apply it with kubectl apply -f to the cluster that runs the workers.',
};

// What the fallback's command tells, for each tab. The check reports how
// the host reaches the coordinator; it does not read the invite.
const CHECK_HINT: Record<WorkerTab, string> = {
  [WorkerTab.linux]:
    'Run this on the worker host. From an empty data directory, it dials even after the worker joined, and shows whether the host reaches the address and what the coordinator answers:',
  [WorkerTab.docker]:
    'Run this on the worker host. From a fresh container, it shows whether the host reaches the address and what the coordinator answers:',
  [WorkerTab.kubernetes]:
    "Run this against the cluster. The worker's log names the cause:",
};

const TABS: { id: WorkerTab; label: string }[] = [
  { id: WorkerTab.linux, label: 'Linux' },
  { id: WorkerTab.docker, label: 'Docker' },
  { id: WorkerTab.kubernetes, label: 'Kubernetes' },
];

/**
 * Step 3: the way to run the worker, its invite, and the wait for it. The
 * tab sets the invite kind: a pod has no lasting data directory, so
 * Kubernetes takes a reusable invite only. The dialog keeps the invites,
 * so one stays on screen when readiness changes after it was created.
 */
export function WorkerStep({
  address,
  workers,
  invites,
  onInvite,
}: {
  /** The validated host:port; undefined while step 2 is not done. */
  address?: string;
  workers: Worker[];
  invites: CreatedInvites;
  onInvite: (kind: WorkerInviteKind, invite: CreatedInvite) => void;
}) {
  const { ts } = useI18n();
  const client = useClient();
  const { version } = useConfig();
  const [tab, setTab] = React.useState(WorkerTab.linux);
  const [labelsInput, setLabelsInput] = React.useState('');
  const [chosenKind, setChosenKind] = React.useState(
    WorkerInviteKind.single_use
  );
  const [creating, setCreating] = React.useState(false);
  const [problem, setProblem] = React.useState<RequestProblem>();

  if (!address && Object.keys(invites).length === 0) return null;

  const kind =
    tab === WorkerTab.kubernetes ? WorkerInviteKind.reusable : chosenKind;
  const invite = invites[kind];
  const labels = normalizeLabels(labelsInput);
  const image = publishedImage(version);
  const panelId = `${TAB_PREFIX}-panel`;
  // The commands use the address an invite was made for, so a later change
  // of the field, by the admin or a new reading, never retargets them.
  const inputFor = (made: CreatedInvite): CommandInput | undefined =>
    'labels' in labels
      ? {
          address: made.address,
          labels: labels.labels,
          inviteId: made.id,
          invite: made.invite,
          image,
        }
      : undefined;
  const panelShowsInvite = !(tab !== WorkerTab.linux && !image);

  const create = async () => {
    if (!address) return;
    setCreating(true);
    setProblem(undefined);
    try {
      const { data, error, response } = await client.POST('/workers/invites', {
        body: { reusable: kind === WorkerInviteKind.reusable, address },
      });
      if (data) {
        onInvite(kind, { ...data, createdAt: Date.now(), address });
      } else {
        setProblem(
          requestProblem(
            response.status,
            error,
            'The coordinator cannot create invites.'
          )
        );
      }
    } catch {
      setProblem({ text: 'The request failed.' });
    } finally {
      setCreating(false);
    }
  };

  return (
    <>
      <TabStrip
        label={ts('How the worker runs')}
        idPrefix={TAB_PREFIX}
        tabs={TABS.map((t) => ({ ...t, controls: panelId }))}
        value={tab}
        onChange={(id) => setTab(id as WorkerTab)}
        touchLabels
      />
      <div
        role="tabpanel"
        id={panelId}
        aria-labelledby={tabStripTabId(TAB_PREFIX, tab)}
        className="space-y-3"
      >
        {tab !== WorkerTab.linux && !image ? (
          <p className="text-sm">
            <I18nText
              text="This server is a development build ({version}), so no published image matches it. Run a released Dagu to add a worker with Docker or Kubernetes, or use the Linux tab."
              values={{ version }}
            />
          </p>
        ) : (
          <WorkerCommand
            tab={tab}
            labelsInput={labelsInput}
            onLabelsChange={setLabelsInput}
            kind={kind}
            onKindChange={setChosenKind}
            invite={invite}
            address={address}
            creating={creating}
            problem={problem}
            onCreate={() => void create()}
            input={invite && inputFor(invite)}
          />
        )}
        {/* Every invite keeps its wait for the life of the dialog, so a
            join is not forgotten when the admin switches tabs. */}
        {Object.values(invites).map((made) => (
          <Arrival
            key={made.id}
            hidden={!panelShowsInvite || made.id !== invite?.id}
            invite={made}
            tab={tab}
            input={inputFor(made)}
            workers={workers}
          />
        ))}
      </div>
    </>
  );
}

function WorkerCommand({
  tab,
  labelsInput,
  onLabelsChange,
  kind,
  onKindChange,
  invite,
  address,
  creating,
  problem,
  onCreate,
  input,
}: {
  tab: WorkerTab;
  labelsInput: string;
  onLabelsChange: (input: string) => void;
  kind: WorkerInviteKind;
  onKindChange: (kind: WorkerInviteKind) => void;
  invite?: CreatedInvite;
  /** The validated address of step 2, if any. */
  address?: string;
  creating: boolean;
  problem?: RequestProblem;
  onCreate: () => void;
  input?: CommandInput;
}) {
  const labels = normalizeLabels(labelsInput);
  const command = input && workerCommand(tab, input);
  return (
    <>
      <div className="space-y-1.5">
        <label className="text-sm" htmlFor="add-worker-labels">
          <I18nText text="Labels (optional)" />
        </label>
        {/* A one-line text area keeps a pasted line break, which the MCP
            action reads too: trimmed at a label's edge, refused inside. */}
        <Textarea
          id="add-worker-labels"
          rows={1}
          className="min-h-9"
          value={labelsInput}
          placeholder="gpu=true,region=eu"
          aria-invalid={'problem' in labels || undefined}
          aria-describedby={
            'problem' in labels ? 'add-worker-labels-problem' : undefined
          }
          onKeyDown={(e) => e.key === 'Enter' && e.preventDefault()}
          onChange={(e) => onLabelsChange(e.target.value)}
        />
        {'problem' in labels && (
          <p
            id="add-worker-labels-problem"
            className="text-xs text-destructive"
          >
            <I18nText
              text={labels.problem.text}
              values={labels.problem.values}
            />
          </p>
        )}
      </div>

      <KindChoice tab={tab} kind={kind} onChange={onKindChange} />

      {!invite ? (
        <div className="space-y-2">
          <Button
            variant="primary"
            size="sm"
            disabled={!address || creating || 'problem' in labels}
            onClick={onCreate}
          >
            {creating && <Spinner />}
            <I18nText text="Create invite" />
          </Button>
          {problem && <ProblemText problem={problem} />}
        </div>
      ) : (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            <InviteTerms invite={invite} kind={kind} />{' '}
            <I18nText text="The invite is shown only here. Closing the dialog keeps it." />
          </p>
          {command && (
            <>
              <p className="text-sm">
                <I18nText text={RUN_WHERE[tab]} />
                {tab === WorkerTab.linux && (
                  <>
                    {' '}
                    <a
                      href={INSTALL_DOCS_URL}
                      target="_blank"
                      rel="noreferrer"
                      className="underline underline-offset-2"
                    >
                      <I18nText text="Installation guide" />
                    </a>
                  </>
                )}
              </p>
              <CommandBlock text={command} label="Copy the command" />
            </>
          )}
          {address && address !== invite.address && (
            <div className="space-y-2 text-sm">
              <p>
                <I18nText
                  text="This invite was made for {address}. To use {next}, create another invite."
                  values={{ address: invite.address, next: address }}
                />
              </p>
              <Button
                size="xs"
                disabled={creating || 'problem' in labels}
                onClick={onCreate}
              >
                {creating && <Spinner />}
                <I18nText text="Create invite" />
              </Button>
            </div>
          )}
          {problem && <ProblemText problem={problem} />}
        </div>
      )}
    </>
  );
}

function KindChoice({
  tab,
  kind,
  onChange,
}: {
  tab: WorkerTab;
  kind: WorkerInviteKind;
  onChange: (kind: WorkerInviteKind) => void;
}) {
  if (tab === WorkerTab.kubernetes) {
    return (
      <p className="text-sm text-muted-foreground">
        <I18nText text="Reusable invite: pods are replaced and scaled, and each new pod joins with it. It lasts until deleted." />
      </p>
    );
  }
  const options = [
    {
      value: WorkerInviteKind.single_use,
      text: 'Single use: one worker that keeps its data directory. Expires after 24 hours unless used.',
    },
    {
      value: WorkerInviteKind.reusable,
      text:
        tab === WorkerTab.docker
          ? 'Reusable: any number of containers, each with its own volume. Lasts until deleted.'
          : 'Reusable: any number of hosts. Lasts until deleted.',
    },
  ];
  return (
    <fieldset className="space-y-1.5">
      <legend className="mb-1.5 text-sm">
        <I18nText text="Invite" />
      </legend>
      {options.map((o) => (
        <label key={o.value} className="flex items-start gap-2 text-sm">
          <input
            type="radio"
            name="add-worker-kind"
            className="mt-1"
            checked={kind === o.value}
            onChange={() => onChange(o.value)}
          />
          <I18nText text={o.text} />
        </label>
      ))}
    </fieldset>
  );
}

function InviteTerms({
  invite,
  kind,
}: {
  invite: CreatedInvite;
  kind: WorkerInviteKind;
}) {
  const expires = invite.expiresAt
    ? new Date(invite.expiresAt).toLocaleString()
    : undefined;
  if (kind === WorkerInviteKind.single_use) {
    return (
      <I18nText
        text="Single-use invite, expires {time}."
        values={{ time: expires ?? '' }}
      />
    );
  }
  return expires ? (
    <I18nText
      text="Reusable invite, expires {time}."
      values={{ time: expires }}
    />
  ) : (
    <I18nText text="Reusable invite, until deleted." />
  );
}

/**
 * Waits for a worker with the invite's ID on the workers list that System
 * status refreshes. A worker once seen stays shown, so a worker that
 * leaves the list later does not bring back the timeout. At
 * ARRIVAL_TIMEOUT_MS without one, it reads the list once itself, since
 * System status may have paused its refresh, and only a complete read
 * without the worker lists the usual causes. A read that fails says so.
 */
function Arrival({
  hidden,
  invite,
  tab,
  input,
  workers,
}: {
  hidden: boolean;
  invite: CreatedInvite;
  tab: WorkerTab;
  input?: CommandInput;
  workers: Worker[];
}) {
  const client = useClient();
  const [seen, setSeen] = React.useState<Worker[]>([]);
  const [outcome, setOutcome] = React.useState<'waiting' | 'absent' | 'unread'>(
    'waiting'
  );
  const [reading, setReading] = React.useState(false);

  const record = React.useCallback(
    (list: Worker[]) => {
      const found = list.filter((w) => w.inviteId === invite.id);
      if (found.length === 0) return false;
      setSeen((prev) => [
        ...prev,
        ...found.filter((w) => !prev.some((p) => p.id === w.id)),
      ]);
      return true;
    },
    [invite.id]
  );

  React.useEffect(() => {
    record(workers);
  }, [workers, record]);

  // One read of the list, for a refresh that System status may have paused.
  // A partial list still shows a worker it holds; without one, it cannot
  // tell that no worker joined.
  const readWorkers = React.useCallback(async () => {
    setReading(true);
    try {
      const { data } = await client.GET('/workers', {
        params: { query: { remoteNode: LOCAL_NODE } },
      });
      const found = record(data?.workers ?? []);
      const complete = !!data && (data.errors ?? []).length === 0;
      if (!found) setOutcome(complete ? 'absent' : 'unread');
    } catch {
      setOutcome('unread');
    } finally {
      setReading(false);
    }
  }, [client, record]);

  const joined = seen.length > 0;
  React.useEffect(() => {
    if (joined) return;
    const left = invite.createdAt + ARRIVAL_TIMEOUT_MS - Date.now();
    const timer = setTimeout(() => void readWorkers(), Math.max(left, 0));
    return () => clearTimeout(timer);
  }, [invite.createdAt, joined, readWorkers]);

  const check = input && checkCommand(tab, input);
  const address = invite.address;
  const port = address.slice(address.lastIndexOf(':') + 1);
  const lookAgain = (
    <Button
      size="xs"
      aria-disabled={reading || undefined}
      onClick={() => {
        if (!reading) void readWorkers();
      }}
    >
      {reading ? <Spinner /> : <RefreshCw />}
      <I18nText text="Look for the worker again" />
    </Button>
  );
  return (
    <div hidden={hidden} className="space-y-2 text-sm">
      {/* One status region stays mounted, so the join is announced. */}
      <p role="status" className="flex items-center gap-2">
        {joined ? (
          <I18nText
            text={
              seen.length === 1
                ? '1 worker joined with this invite.'
                : '{count} workers joined with this invite.'
            }
            values={{ count: seen.length }}
          />
        ) : outcome === 'waiting' || reading ? (
          <span className="flex items-center gap-2 text-muted-foreground">
            <Spinner />
            <I18nText
              text={
                reading
                  ? 'Looking for the worker...'
                  : 'Waiting for the worker to join...'
              }
            />
          </span>
        ) : (
          <I18nText
            text={
              outcome === 'absent'
                ? 'No worker has joined yet. The usual causes:'
                : 'The workers list could not be read, so the dialog cannot tell whether the worker joined.'
            }
          />
        )}
      </p>
      {joined && (
        <ul className="space-y-1">
          {seen.map((w) => (
            <li key={w.id} className="flex flex-wrap items-baseline gap-x-2">
              <span className="font-mono font-medium">{w.id}</span>
              {Object.entries(w.labels ?? {}).map(([key, value]) => (
                <span key={key} className="text-xs text-muted-foreground">
                  {value ? `${key}=${value}` : key}
                </span>
              ))}
            </li>
          ))}
        </ul>
      )}
      {!joined && outcome === 'unread' && lookAgain}
      {!joined && outcome === 'absent' && (
        <>
          <ul className="list-disc space-y-0.5 pl-5">
            <li>
              <I18nText
                text="A firewall or security group closes port {port}."
                values={{ port }}
              />
            </li>
            <li>
              <I18nText
                text="The container that runs Dagu does not publish port {port}."
                values={{ port }}
              />
            </li>
            <li>
              <I18nText
                text="The worker cannot reach {address}."
                values={{ address }}
              />
            </li>
          </ul>
          {check && (
            <>
              <p>
                <I18nText text={CHECK_HINT[tab]} />
              </p>
              <CommandBlock text={check} label="Copy the command" />
            </>
          )}
          {lookAgain}
        </>
      )}
    </div>
  );
}
