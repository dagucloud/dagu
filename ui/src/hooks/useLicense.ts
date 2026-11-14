import { useConfig } from '@/contexts/ConfigContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { useContext } from 'react';
import { hasActiveLicense } from '@/lib/license';
import { LicenseContext } from '@/contexts/LicenseContext';

const defaultLicense: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};

export function useLicense(): LicenseStatus {
  const config = useConfig();
  const state = useContext(LicenseContext);
  return (state ? state.license : config?.license) ?? defaultLicense;
}

export function useLicenseState() {
  const state = useContext(LicenseContext);
  const license = useLicense();
  return {
    license,
    loading: Boolean(state && !state.license && !state.error),
    error: state?.error,
    now: state?.now ?? Date.now(),
  };
}

export function useHasFeature(feature: string): boolean {
  const license = useLicense();
  return license.features.includes(feature) && hasActiveLicense(license);
}
