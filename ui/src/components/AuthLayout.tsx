// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { DaguMark } from '@/components/DaguMark';
import { LanguageSelector } from '@/components/LanguageSelector';
import { useConfig } from '@/contexts/ConfigContext';

type AuthLayoutProps = {
  subtitle: ReactNode;
  children: ReactNode;
};

/** Centered card layout shared by the sign-in and first-run setup pages. */
export function AuthLayout({ subtitle, children }: AuthLayoutProps) {
  const config = useConfig();

  return (
    <main className="flex min-h-screen flex-col items-center justify-center bg-background px-4 py-12">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex flex-col items-center gap-4 text-center">
          <DaguMark className="h-10 w-10 text-foreground" />
          <div className="space-y-1">
            <h1 className="text-xl font-semibold tracking-tight">
              {config.title || 'Dagu'}
            </h1>
            <p className="text-sm text-muted-foreground">{subtitle}</p>
          </div>
        </div>
        <div className="space-y-4 rounded-xl border border-border bg-card p-6 shadow-sm">
          {children}
        </div>
        <div className="mt-6 flex justify-center">
          <LanguageSelector />
        </div>
      </div>
    </main>
  );
}
