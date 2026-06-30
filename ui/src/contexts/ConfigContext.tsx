import { createContext, useContext } from 'react';
import type { components } from '../api/v1/schema';

export type PathsConfig = {
  dagsDir: string;
  logDir: string;
  suspendFlagsDir: string;
  adminLogsDir: string;
  baseConfig: string;
  dagRunsDir: string;
  queueDir: string;
  procDir: string;
  serviceRegistryDir: string;
  configFileUsed: string;
  gitSyncDir: string;
  auditLogsDir: string;
};

export type AuthMode = 'none' | 'basic' | 'builtin';

export type LicenseStatus = {
  valid: boolean;
  plan: string;
  expiry: string;
  features: string[];
  gracePeriod: boolean;
  graceEndsAt?: string;
  community: boolean;
  source: string;
  warningCode: string;
};

export type WorkspaceResponse = components['schemas']['WorkspaceResponse'];

export type Config = {
  apiURL: string;
  basePath: string;
  title: string;
  navbarColor: string;
  tz: string;
  tzOffsetInSec: number | undefined;
  version: string;
  maxDashboardPageLimit: number;
  remoteNodes: string;
  initialWorkspaces: WorkspaceResponse[];
  authMode: AuthMode;
  setupRequired: boolean;
  oidcEnabled: boolean;
  oidcButtonLabel: string;
  terminalEnabled: boolean;
  gitSyncEnabled: boolean;
  updateAvailable: boolean;
  latestVersion: string;
  permissions: {
    writeDags: boolean;
    runDags: boolean;
  };
  license: LicenseStatus;
  paths: PathsConfig;
};

export const ConfigContext = createContext<Config>(null!);

export const ConfigUpdateContext = createContext<
  (patch: Partial<Config>) => void
>(() => {});

/**
 * Access the application configuration from the nearest ConfigContext provider.
 *
 * @returns The Config object containing API URL, paths, permissions, and feature flags.
 */
export function useConfig(): Config {
  return useContext(ConfigContext);
}

export function useUpdateConfig(): (patch: Partial<Config>) => void {
  return useContext(ConfigUpdateContext);
}
