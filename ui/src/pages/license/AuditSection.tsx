// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { Info } from 'lucide-react';
import type { LicenseMonitoring } from '@/hooks/useLicenseMonitoring';
import { useI18n } from '@/i18n/I18nProvider';
import { auditLevelLabels } from '@/lib/license';

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </>
  );
}

/**
 * Shows which audit entries this server sends to Dagu Console, as cloud.audit
 * in the configuration sets, what each entry carries, and where Dagu Console
 * keeps them.
 */
export function AuditSection({
  monitoring,
}: {
  monitoring?: LicenseMonitoring;
}) {
  const { ts } = useI18n();
  if (!monitoring) {
    return null;
  }

  return (
    <section
      id="audit"
      className="card-obsidian p-4 space-y-3 scroll-mt-4"
      aria-labelledby="license-audit"
    >
      <div className="space-y-1">
        <h2 id="license-audit" className="text-sm font-medium">
          {ts('Audit logs')}
        </h2>
        <p className="text-xs text-muted-foreground">
          {ts(
            "Dagu Console keeps a copy of this server's audit entries, so the record of who did what outlives the server."
          )}
        </p>
      </div>
      <dl className="grid grid-cols-[7.5rem_minmax(0,1fr)] items-center gap-x-4 gap-y-1.5 text-sm">
        <Row label={ts('Sends')}>
          {ts(auditLevelLabels[monitoring.auditLevel])}
        </Row>
      </dl>
      <p className="text-sm text-muted-foreground">
        {ts(
          'Each entry sent carries its time, category, action, and result; the user ID and name; the IP address; the resource, workspace, credential, and MCP tool involved; and its details. Secret values and tokens are masked when an entry is written and again before it is sent.'
        )}
      </p>
      <p className="text-sm text-muted-foreground">
        {ts(
          "Dagu Console stores them in the United States, for the workspace's audit retention."
        )}
      </p>
      <p className="text-sm text-muted-foreground flex items-start gap-2">
        <Info className="h-4 w-4 shrink-0 mt-0.5" />
        {ts(
          'cloud.audit in the server configuration sets this. It takes effect with local audit on and a plan that includes audit logs. Change it there and restart Dagu.'
        )}
      </p>
    </section>
  );
}
