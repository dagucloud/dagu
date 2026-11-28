// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  PlaintextReason,
  WorkerConnection as Connection,
  type components,
} from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';

type Worker = components['schemas']['Worker'];

const TRANSPORT_DOCS_URL =
  'https://docs.dagu.sh/server-admin/distributed/transport-security';

// How each h2c reason reads, and the one change that moves the process to
// TLS (Spec 108 §Workers Page). A setting is shown as written, untranslated.
const reasons: Record<
  PlaintextReason,
  {
    text: string;
    change: string;
    setting?: string;
  }
> = {
  [PlaintextReason.peer_insecure]: {
    text: '{setting} in its config',
    change: 'Remove {setting} to use TLS',
    setting: 'peer.insecure: true',
  },
  [PlaintextReason.no_TLS_source]: {
    text: 'no TLS source',
    change: 'Set {setting} to use TLS',
    setting: 'peer.ca_cert_file',
  },
  [PlaintextReason.tls_failed_on_the_network_path]: {
    text: 'TLS failed on the network path',
    change: 'Let its network path pass TLS',
  },
  [PlaintextReason.older_version]: {
    text: 'older version',
    change: 'Upgrade it to use TLS',
  },
};

// WorkerConnection shows how a worker's heartbeat reaches the coordinator.
export function WorkerConnection({ worker }: { worker: Worker }) {
  switch (worker.connection) {
    case Connection.certificate:
      return (
        <span className="text-xs">
          <I18nText text="Certificate" />
        </span>
      );
    case Connection.tls:
      return <span className="text-xs">TLS</span>;
    case Connection.plaintext:
      return <PlaintextConnection reason={worker.plaintextReason} />;
    default:
      return (
        <span className="text-xs text-muted-foreground">
          <I18nText text="Connection not reported" />
        </span>
      );
  }
}

// PlaintextConnection shows why a process uses h2c and the change to make,
// as a neutral line: h2c can be on purpose, as behind a service mesh.
export function PlaintextConnection({ reason }: { reason?: PlaintextReason }) {
  const shown = reason ? reasons[reason] : undefined;
  const values = shown?.setting ? { setting: shown.setting } : undefined;
  return (
    <div className="min-w-0 text-xs">
      <div>
        <a
          href={TRANSPORT_DOCS_URL}
          target="_blank"
          rel="noreferrer"
          className="underline underline-offset-2 decoration-muted-foreground/50 hover:decoration-foreground"
          onClick={(event) => event.stopPropagation()}
        >
          <I18nText text="Plaintext" />
        </a>
        {shown && (
          <>
            {' · '}
            <I18nText text={shown.text} values={values} />
          </>
        )}
      </div>
      {shown && (
        <div className="text-muted-foreground">
          <I18nText text={shown.change} values={values} />
        </div>
      )}
    </div>
  );
}
