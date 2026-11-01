// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useContext, useId, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { BellRing, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useLicenseState } from '@/hooks/useLicense';
import { useStoredValue } from '@/hooks/useStoredValue';
import { useI18n } from '@/i18n/I18nProvider';
import { isLicenseManagedByEnv } from '@/lib/license';
import { cn } from '@/lib/utils';

const DISMISSED = 'dismissed';

export interface MonitoringOfferState {
  visible: boolean;
  /** Hides the offer everywhere for the selected server. */
  dismiss(): void;
}

/**
 * Gates of the monitoring offer: an administrator of a Community server whose
 * license is loaded and not set by the environment, and who has not dismissed
 * the offer for the selected server.
 */
export function useMonitoringOfferState(): MonitoringOfferState {
  const isAdmin = useIsAdmin();
  const { license, loading, error } = useLicenseState();
  const { selectedRemoteNode } = useContext(AppBarContext);
  const [state, setState] = useStoredValue(
    `dagu.monitoringOffer:${selectedRemoteNode || 'local'}`
  );
  const visible =
    isAdmin &&
    !loading &&
    !error &&
    license.community &&
    !isLicenseManagedByEnv(license) &&
    state !== DISMISSED;
  const dismiss = useCallback(() => setState(DISMISSED), [setState]);
  return useMemo(() => ({ visible, dismiss }), [visible, dismiss]);
}

/**
 * Offers administrators of a Community server free monitoring from Dagu
 * Console where alerts matter. Dismissing it hides it everywhere for the
 * selected server.
 */
export function MonitoringOffer({ className }: { className?: string }) {
  const { ts } = useI18n();
  const headingId = useId();
  const { visible, dismiss } = useMonitoringOfferState();

  if (!visible) return null;

  return (
    <section
      aria-labelledby={headingId}
      className={cn('card-obsidian p-4 text-left', className)}
    >
      <div className="flex items-start gap-3">
        <BellRing
          className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground"
          aria-hidden="true"
        />
        <div className="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-3">
          <div className="min-w-0 flex-1 basis-64 space-y-1">
            <h2 id={headingId} className="text-sm font-medium">
              {ts('Get an email if this server stops')}
            </h2>
            <p className="text-sm text-muted-foreground">
              {ts('Alerts from this server stop if the server goes down.')}{' '}
              {ts(
                'Dagu Console emails you when a workflow fails or this server stops reporting. Free for one server.'
              )}
            </p>
          </div>
          <Button asChild size="sm" variant="primary">
            <Link to="/license#monitoring">{ts('Set up for free')}</Link>
          </Button>
        </div>
        <button
          type="button"
          onClick={dismiss}
          className="shrink-0 rounded p-0.5 text-muted-foreground hover:text-foreground"
          aria-label={ts('Dismiss')}
        >
          <X className="h-4 w-4" />
        </button>
      </div>
    </section>
  );
}
