// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState } from 'react';
import { BellRing, X } from 'lucide-react';
import { LicenseMonitoringLevel } from '@/api/v1/schema';
import { MonitoringDialog } from '@/components/MonitoringDialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useLicenseState } from '@/hooks/useLicense';
import {
  shouldAskMonitoring,
  shouldOfferErrors,
  useLicenseMonitoring,
} from '@/hooks/useLicenseMonitoring';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * Asks administrators of a server licensed through Dagu Console what it
 * should report, until they choose or dismiss the question. Once they chose
 * run status, it offers once to also send error messages, so that Dagu
 * Console can show why runs fail.
 */
export function MonitoringNotice() {
  const { ts } = useI18n();
  const isAdmin = useIsAdmin();
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  const { license, loading, error } = useLicenseState();
  const online = isAdmin && !loading && !error && Boolean(license.serverId);
  const { monitoring, setLevel, dismissNotice, dismissErrorsNotice } =
    useLicenseMonitoring(remoteNode, { enabled: online });
  const [choosing, setChoosing] = useState(false);

  const asking =
    shouldAskMonitoring(monitoring) && !monitoring?.noticeDismissed;
  const offeringErrors = shouldOfferErrors(monitoring);
  if (!online || (!asking && !offeringErrors)) return null;

  async function dismiss() {
    setChoosing(false);
    try {
      await (asking ? dismissNotice() : dismissErrorsNotice());
    } catch (err) {
      showError(
        err instanceof Error && err.message
          ? err.message
          : ts('Could not dismiss the notice.'),
        ts('Please try again or check the server connection.')
      );
    }
  }

  return (
    <div
      role="status"
      className="border-b border-primary/20 bg-primary/5 px-4 py-1.5 flex items-center justify-between gap-2 text-sm"
    >
      <span className="flex items-center gap-2">
        <BellRing className="h-4 w-4 shrink-0" aria-hidden="true" />
        {ts(
          asking
            ? 'Monitor this server from Dagu Console'
            : 'Show why runs fail in Dagu Console'
        )}
      </span>
      <span className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          className="underline hover:no-underline"
          onClick={() => setChoosing(true)}
        >
          {ts(asking ? 'Choose what to report' : 'Send error messages')}
        </button>
        <button
          type="button"
          onClick={() => void dismiss()}
          className="p-0.5 rounded"
          aria-label={ts('Dismiss monitoring notice')}
        >
          <X className="h-4 w-4" />
        </button>
      </span>
      <MonitoringDialog
        open={choosing}
        level={monitoring?.level}
        suggested={offeringErrors ? LicenseMonitoringLevel.errors : undefined}
        prompt={asking}
        onSave={async (level) => {
          await setLevel(level);
          setChoosing(false);
        }}
        onCancel={() => void dismiss()}
      />
    </div>
  );
}
