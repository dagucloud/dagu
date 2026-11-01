// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Shield } from 'lucide-react';
import { licensedFeatures, type LicensedFeature } from '@/lib/license';
import { useI18n } from '@/i18n/I18nProvider';
import { LicenseActions } from './LicenseActions';
import { EmptyState } from './PageStates';

export function LicenseFeaturePrompt({
  feature,
  inline = false,
  headingLevel,
}: {
  feature: LicensedFeature;
  /** Renders a one-row callout above other page content. */
  inline?: boolean;
  /** Use 1 when the prompt replaces the whole page. */
  headingLevel?: 1 | 2;
}) {
  const { ts } = useI18n();
  const info = licensedFeatures.find((item) => item.id === feature)!;
  if (!inline) {
    return (
      <EmptyState
        headingLevel={headingLevel}
        icon={<Shield />}
        title={ts(info.title)}
        description={ts(info.description)}
        action={<LicenseActions content={feature} />}
      />
    );
  }
  return (
    <section className="flex flex-col gap-3 rounded-md border border-border bg-card p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="flex items-start gap-3">
        <Shield
          className="mt-0.5 h-5 w-5 shrink-0 text-primary"
          aria-hidden="true"
        />
        <div>
          <h2 className="text-base font-semibold">{ts(info.title)}</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {ts(info.description)}
          </p>
        </div>
      </div>
      <LicenseActions content={feature} />
    </section>
  );
}
