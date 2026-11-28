// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { PlaintextConnection } from '@/features/workers/components/WorkerConnection';
import { I18nText } from '@/i18n/I18nText';

type H2CCaller = components['schemas']['H2CCaller'];

// H2CCallers lists the hosts other than workers that called a coordinator
// over h2c in the last 10 minutes, such as a scheduler, a server, or a CLI.
function H2CCallers({ callers }: { callers: H2CCaller[] }) {
  return (
    <div className="px-3 pb-2 pl-7 text-xs">
      <div className="mb-1 text-muted-foreground">
        <I18nText text="Plaintext calls in the last 10 minutes" />
      </div>
      <ul className="space-y-1">
        {callers.map((caller) => (
          <li
            key={`${caller.host} ${caller.reason}`}
            className="flex items-start gap-3"
          >
            <span className="w-32 shrink-0 truncate font-mono text-muted-foreground">
              {caller.host}
            </span>
            <div className="min-w-0 flex-1">
              <PlaintextConnection reason={caller.reason} />
            </div>
            <RelativeTime
              className="shrink-0 text-muted-foreground"
              timestamp={caller.lastCallAt}
              compact
              ago
            />
          </li>
        ))}
      </ul>
    </div>
  );
}

export default H2CCallers;
