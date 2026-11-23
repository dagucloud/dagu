// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { X } from 'lucide-react';
import React, { useState } from 'react';
import {
  keyOffers,
  nodeKey,
  type KeyOffer,
  type NodeTestRun,
} from './useNodeTests';

type ConnectionTest = components['schemas']['InventoryNodeConnectionTest'];
type HostKeyTrust = components['schemas']['InventoryHostKeyTrust'];

type NodeTestPanelProps = {
  run: NodeTestRun;
  results: Record<string, ConnectionTest>;
  trusts: Record<string, HostKeyTrust>;
  trusting: boolean;
  canTrust: boolean;
  onTrust: (offers: KeyOffer[]) => void;
  onClose: () => void;
};

/**
 * Shows each node's test result as it arrives, and offers to trust the host
 * keys that stopped a test: unknown keys together, a changed key on its
 * own, after showing the trusted and presented fingerprints.
 */
export function NodeTestPanel({
  run,
  results,
  trusts,
  trusting,
  canTrust,
  onTrust,
  onClose,
}: NodeTestPanelProps): React.ReactElement {
  const [confirming, setConfirming] = useState<KeyOffer[] | null>(null);
  const finished = run.names.filter((name) => !run.pending.has(nodeKey(name)));
  const passed = finished.filter((name) => results[nodeKey(name)]?.ok).length;
  const failed = finished.filter(
    (name) => results[nodeKey(name)]?.ok === false
  ).length;
  const offers =
    canTrust && run.pending.size === 0 && !trusting
      ? keyOffers(finished, results)
      : [];
  const unknown = offers.filter((offer) => !offer.trusted);

  return (
    <section
      aria-labelledby="node-test-title"
      className="card-obsidian flex max-h-72 shrink-0 flex-col text-xs"
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <h2 id="node-test-title" className="font-semibold">
          <I18nText text={'Connection test'} />
        </h2>
        <span className="text-muted-foreground" aria-live="polite">
          {run.pending.size > 0 ? (
            <I18nTemplate
              text="Tested {done} of {total}"
              values={{ done: finished.length, total: run.names.length }}
            />
          ) : run.names.length > 0 ? (
            <I18nTemplate
              text="{passed} passed, {failed} failed"
              values={{ passed, failed }}
            />
          ) : null}
        </span>
        <div className="ml-auto flex items-center gap-2">
          {unknown.length > 1 && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => setConfirming(unknown)}
            >
              <I18nTemplate
                text="Trust {count} unknown keys"
                values={{ count: unknown.length }}
              />
            </Button>
          )}
          <I18nProps>
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7"
              aria-label="Close"
              onClick={onClose}
            >
              <X className="h-4 w-4" />
            </Button>
          </I18nProps>
        </div>
      </div>
      {run.error && (
        <p role="alert" className="px-3 py-2 text-error">
          {run.error}
        </p>
      )}
      <ul className="min-h-0 flex-1 divide-y divide-border/60 overflow-y-auto">
        {run.names.map((name) => (
          <TestRow
            key={name}
            name={name}
            pending={run.pending.has(nodeKey(name))}
            result={results[nodeKey(name)]}
            trusts={trusts}
            offers={offers}
            onTrust={(offer) => setConfirming([offer])}
          />
        ))}
      </ul>
      {confirming && (
        <TrustDialog
          offers={confirming}
          onCancel={() => setConfirming(null)}
          onConfirm={() => {
            onTrust(confirming);
            setConfirming(null);
          }}
        />
      )}
    </section>
  );
}

function TestRow({
  name,
  pending,
  result,
  trusts,
  offers,
  onTrust,
}: {
  name: string;
  pending: boolean;
  result?: ConnectionTest;
  trusts: Record<string, HostKeyTrust>;
  offers: KeyOffer[];
  onTrust: (offer: KeyOffer) => void;
}): React.ReactElement {
  const refused = result?.hostKey;
  const offer =
    refused && offers.find((o) => nodeKey(o.node) === nodeKey(refused.node));
  // Once a trusted key lets the node pass, the row keeps the node's trust.
  const trust = trusts[nodeKey(refused?.node ?? name)];

  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-1.5">
      <span className="w-40 truncate font-mono font-medium">{name}</span>
      {pending ? (
        <span className="text-muted-foreground">
          <I18nText text={'Testing...'} />
        </span>
      ) : result ? (
        <TestOutcome result={result} />
      ) : (
        // The request failed before this node's result arrived.
        <span className="text-muted-foreground">
          <I18nText text={'Not tested'} />
        </span>
      )}
      {!pending && trust && <TrustOutcome trust={trust} />}
      {!pending && offer && (
        <Button
          size="sm"
          variant="outline"
          className="ml-auto h-6"
          onClick={() => onTrust(offer)}
        >
          {offer.trusted ? (
            <I18nText text={'Replace key'} />
          ) : (
            <I18nText text={'Trust key'} />
          )}
        </Button>
      )}
    </li>
  );
}

/** The outcome of one node's test. */
export function TestOutcome({
  result,
}: {
  result: ConnectionTest;
}): React.ReactElement {
  if (result.ok) {
    return (
      <span className="flex min-w-0 items-center gap-2">
        <Badge variant="success">
          <I18nText text={'Passed'} />
        </Badge>
        <span className="truncate font-mono text-muted-foreground">
          {result.fingerprint}
        </span>
        <span className="text-muted-foreground">{result.durationMs} ms</span>
      </span>
    );
  }
  return (
    <span className="flex min-w-0 flex-1 items-center gap-2">
      <Badge variant="error">
        {result.hostKey?.status === 'changed' ? (
          <I18nText text={'Changed key'} />
        ) : result.hostKey ? (
          <I18nText text={'Unknown key'} />
        ) : (
          <I18nText text={'Failed'} />
        )}
      </Badge>
      <span className="min-w-0 break-all text-muted-foreground">
        {result.error}
      </span>
    </span>
  );
}

function TrustOutcome({ trust }: { trust: HostKeyTrust }): React.ReactElement {
  if (trust.error) {
    return <span className="break-all text-error">{trust.error}</span>;
  }
  return (
    <Badge variant="outline">
      {trust.outcome === 'replaced' ? (
        <I18nText text={'Key replaced'} />
      ) : (
        <I18nText text={'Key trusted'} />
      )}
    </Badge>
  );
}

/**
 * Asks before keys are trusted. A changed key comes alone, with the key it
 * replaces, and the focus starts on Cancel.
 */
function TrustDialog({
  offers,
  onCancel,
  onConfirm,
}: {
  offers: KeyOffer[];
  onCancel: () => void;
  onConfirm: () => void;
}): React.ReactElement {
  const { ts } = useI18n();
  const replaced = offers.find((offer) => offer.trusted);

  if (replaced) {
    return (
      <ConfirmDialog
        visible
        title={ts('Replace trusted host key')}
        buttonText={ts('Replace key')}
        initialFocus="cancel"
        dismissModal={onCancel}
        onSubmit={onConfirm}
      >
        <div className="space-y-3 text-sm">
          <p>
            <I18nTemplate
              text="The host key of {node} has changed. A host that was reinstalled presents a new key, and so does a connection someone intercepts. Replace the key only if you know why it changed."
              values={{ node: <code>{replaced.node}</code> }}
            />
          </p>
          <FingerprintList
            rows={[
              [ts('Trusted'), replaced.trusted ?? ''],
              [ts('Presented'), replaced.presented],
            ]}
          />
        </div>
      </ConfirmDialog>
    );
  }
  return (
    <ConfirmDialog
      visible
      title={ts('Trust host keys')}
      buttonText={ts('Trust')}
      dismissModal={onCancel}
      onSubmit={onConfirm}
    >
      <div className="space-y-3 text-sm">
        <p>
          <I18nText
            text={
              'Runs and tests will accept these keys. Trust them only if they are the keys of these hosts.'
            }
          />
        </p>
        <FingerprintList
          rows={offers.map((offer) => [offer.node, offer.presented])}
        />
      </div>
    </ConfirmDialog>
  );
}

function FingerprintList({
  rows,
}: {
  rows: [string, string][];
}): React.ReactElement {
  return (
    <dl className="max-h-60 space-y-1 overflow-y-auto font-mono text-xs">
      {rows.map(([label, fingerprint]) => (
        <div key={label} className="grid grid-cols-[8rem_minmax(0,1fr)] gap-2">
          <dt className="truncate text-muted-foreground">{label}</dt>
          <dd className="break-all">{fingerprint}</dd>
        </div>
      ))}
    </dl>
  );
}
