// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { WorkerVersionDrift, type components } from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';

type Worker = components['schemas']['Worker'];

// WorkerVersion shows the Dagu version a worker runs and whether it differs
// from the server's.
export function WorkerVersion({ worker }: { worker: Worker }) {
  if (!worker.version) {
    return (
      <span className="text-xs text-muted-foreground">
        <I18nText text="Version not reported" />
      </span>
    );
  }
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-1 text-xs">
      <span className="min-w-0 whitespace-normal break-all font-mono text-muted-foreground">
        {worker.version.startsWith('v') ? worker.version : `v${worker.version}`}
      </span>
      {worker.versionDrift === WorkerVersionDrift.behind && (
        <span className="text-warning">
          <I18nText text="older than the server" />
        </span>
      )}
      {worker.versionDrift === WorkerVersionDrift.ahead && (
        <span className="text-muted-foreground">
          <I18nText text="newer than the server" />
        </span>
      )}
    </span>
  );
}

// countWorkersBehind counts the workers that run an older Dagu version than
// the server.
export function countWorkersBehind(workers: Worker[]): number {
  return workers.filter(
    (worker) => worker.versionDrift === WorkerVersionDrift.behind
  ).length;
}
