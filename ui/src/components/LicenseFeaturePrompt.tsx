// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Shield } from 'lucide-react';
import { licensedFeatures, type LicensedFeature } from '@/lib/license';
import { useI18n } from '@/i18n/I18nProvider';
import { LicenseActions } from './LicenseActions';

export function LicenseFeaturePrompt({
  feature,
}: {
  feature: LicensedFeature;
}) {
  const { ts } = useI18n();
  const info = licensedFeatures.find((item) => item.id === feature)!;
  return (
    <section className="rounded-md border border-border bg-card p-4 space-y-3">
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
