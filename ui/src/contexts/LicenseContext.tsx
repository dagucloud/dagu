// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { createContext } from 'react';
import type { KeyedMutator } from 'swr';
import type { components } from '@/api/v1/schema';
import type { LicenseStatus } from './ConfigContext';

type LicenseResponse = components['schemas']['LicenseStatusResponse'];

type LicenseState = {
  license?: LicenseStatus;
  error?: unknown;
  now?: number;
  mutate: KeyedMutator<LicenseResponse>;
};

export const LicenseContext = createContext<LicenseState | null>(null);
