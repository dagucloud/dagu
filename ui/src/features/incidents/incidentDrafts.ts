// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  IncidentPolicyScope,
  IncidentProviderType,
  IncidentSeverity,
} from '@/api/v1/schema';

export type IncidentProvider = components['schemas']['IncidentProvider'];
export type IncidentProviderInput =
  components['schemas']['IncidentProviderInput'];
export type IncidentPolicy = components['schemas']['IncidentPolicy'];
export type IncidentPolicyInput = components['schemas']['IncidentPolicyInput'];
export type IncidentPolicySet = components['schemas']['IncidentPolicySet'];
export type IncidentPolicySetInput =
  components['schemas']['IncidentPolicySetInput'];

export type DraftProvider = {
  id?: string;
  name: string;
  type: IncidentProviderType;
  enabled: boolean;
  routingKey: string;
  routingKeyConfigured: boolean;
  routingKeyPreview: string;
  webhookUrl: string;
  webhookUrlConfigured: boolean;
  webhookUrlPreview: string;
  allowInsecureHttp: boolean;
  allowPrivateNetwork: boolean;
};

export type DraftIncidentPolicy = {
  id?: string;
  providerId: string;
  enabled: boolean;
  severity: IncidentSeverity;
  resolveOnRecovery: boolean;
  dedupKeyTemplate: string;
  messageTemplate: string;
  descriptionTemplate: string;
};

export type DraftIncidentPolicySet = {
  id?: string;
  scope: IncidentPolicyScope;
  workspace?: string;
  dagName?: string;
  enabled: boolean;
  inheritParent: boolean;
  policies: DraftIncidentPolicy[];
  createdAt?: string;
  updatedAt?: string;
};

export type IncidentRoutingMode = 'inherit' | 'custom' | 'off';

export const DEFAULT_INCIDENT_DEDUP_KEY_TEMPLATE =
  'dagu:workspace:{{workspace}}:dag:{{dag.name}}:failure';
export const DEFAULT_INCIDENT_MESSAGE_TEMPLATE = 'Dagu DAG {{dag.name}} failed';
export const DEFAULT_INCIDENT_DESCRIPTION_TEMPLATE =
  'Run {{run.id}} finished with status {{run.status}}.\n{{run.link}}\n{{run.error}}';

export const INCIDENT_PROVIDER_TYPES = [
  IncidentProviderType.pagerduty,
  IncidentProviderType.solarwinds_incident_response,
] as const;

export const INCIDENT_SEVERITIES = [
  IncidentSeverity.critical,
  IncidentSeverity.error,
  IncidentSeverity.warning,
  IncidentSeverity.info,
] as const;

export function providerLabel(type: IncidentProviderType): string {
  switch (type) {
    case IncidentProviderType.pagerduty:
      return 'PagerDuty';
    case IncidentProviderType.solarwinds_incident_response:
      return 'SolarWinds Incident Response';
    default:
      return String(type);
  }
}

export function severityLabel(severity: IncidentSeverity): string {
  switch (severity) {
    case IncidentSeverity.critical:
      return 'Critical';
    case IncidentSeverity.error:
      return 'Error';
    case IncidentSeverity.warning:
      return 'Warning';
    case IncidentSeverity.info:
      return 'Info';
    default:
      return String(severity);
  }
}

export function severityBadgeClass(severity: IncidentSeverity): string {
  switch (severity) {
    case IncidentSeverity.critical:
      return 'status-failed';
    case IncidentSeverity.error:
      return 'status-aborted';
    case IncidentSeverity.warning:
      return 'status-running';
    case IncidentSeverity.info:
      return 'status-neutral';
    default:
      return 'status-neutral';
  }
}

export function blankProvider(
  type = IncidentProviderType.pagerduty
): DraftProvider {
  return {
    name: providerLabel(type),
    type,
    enabled: true,
    routingKey: '',
    routingKeyConfigured: false,
    routingKeyPreview: '',
    webhookUrl: '',
    webhookUrlConfigured: false,
    webhookUrlPreview: '',
    allowInsecureHttp: false,
    allowPrivateNetwork: false,
  };
}

export function providerDraftFromAPI(
  provider: IncidentProvider
): DraftProvider {
  return {
    id: provider.id,
    name: provider.name,
    type: provider.type,
    enabled: provider.enabled,
    routingKey: '',
    routingKeyConfigured: !!provider.pagerDuty?.routingKeyConfigured,
    routingKeyPreview: provider.pagerDuty?.routingKeyPreview || '',
    webhookUrl: '',
    webhookUrlConfigured: !!provider.solarWinds?.webhookUrlConfigured,
    webhookUrlPreview: provider.solarWinds?.webhookUrlPreview || '',
    allowInsecureHttp: !!provider.solarWinds?.allowInsecureHttp,
    allowPrivateNetwork: !!provider.solarWinds?.allowPrivateNetwork,
  };
}

export function providerInput(draft: DraftProvider): IncidentProviderInput {
  const input: IncidentProviderInput = {
    name: draft.name.trim(),
    type: draft.type,
    enabled: draft.enabled,
  };
  if (draft.type === IncidentProviderType.pagerduty) {
    input.pagerDuty = {
      routingKey: draft.routingKey.trim() || undefined,
    };
  }
  if (draft.type === IncidentProviderType.solarwinds_incident_response) {
    input.solarWinds = {
      webhookUrl: draft.webhookUrl.trim() || undefined,
      allowInsecureHttp: draft.allowInsecureHttp || undefined,
      allowPrivateNetwork: draft.allowPrivateNetwork || undefined,
    };
  }
  return input;
}

export function blankPolicy(providerId = ''): DraftIncidentPolicy {
  return {
    providerId,
    enabled: true,
    severity: IncidentSeverity.error,
    resolveOnRecovery: true,
    dedupKeyTemplate: DEFAULT_INCIDENT_DEDUP_KEY_TEMPLATE,
    messageTemplate: DEFAULT_INCIDENT_MESSAGE_TEMPLATE,
    descriptionTemplate: DEFAULT_INCIDENT_DESCRIPTION_TEMPLATE,
  };
}

export function policySetDraftFromAPI(
  policySet?: IncidentPolicySet,
  fallbackScope: IncidentPolicyScope = IncidentPolicyScope.global,
  fallbackWorkspace?: string,
  fallbackDAGName?: string
): DraftIncidentPolicySet {
  const hasSavedPolicySet = !!policySet?.id;
  return {
    id: policySet?.id,
    scope: policySet?.scope || fallbackScope,
    workspace: policySet?.workspace || fallbackWorkspace,
    dagName: policySet?.dagName || fallbackDAGName,
    enabled: hasSavedPolicySet
      ? (policySet?.enabled ?? true)
      : fallbackScope !== IncidentPolicyScope.global,
    inheritParent:
      policySet?.inheritParent ?? fallbackScope !== IncidentPolicyScope.global,
    policies: (policySet?.policies || []).map(policyDraftFromAPI),
    createdAt: policySet?.createdAt,
    updatedAt: policySet?.updatedAt,
  };
}

export function policySetInput(
  draft: DraftIncidentPolicySet
): IncidentPolicySetInput {
  const mode = incidentRoutingMode(draft);
  if (mode === 'inherit') {
    return {
      enabled: true,
      inheritParent: true,
      policies: [],
    };
  }
  if (mode === 'off') {
    return {
      enabled: false,
      inheritParent: false,
      policies: [],
    };
  }
  return {
    enabled: true,
    inheritParent: false,
    policies: draft.policies
      .filter((policy) => policy.providerId)
      .map(policyInput),
  };
}

export function incidentRoutingMode(
  draft: DraftIncidentPolicySet
): IncidentRoutingMode {
  if (draft.inheritParent) return 'inherit';
  if (!draft.enabled) return 'off';
  return 'custom';
}

export function withIncidentRoutingMode(
  draft: DraftIncidentPolicySet,
  mode: IncidentRoutingMode
): DraftIncidentPolicySet {
  switch (mode) {
    case 'inherit':
      return { ...draft, enabled: true, inheritParent: true, policies: [] };
    case 'off':
      return { ...draft, enabled: false, inheritParent: false, policies: [] };
    case 'custom':
    default:
      return { ...draft, enabled: true, inheritParent: false };
  }
}

function policyDraftFromAPI(policy: IncidentPolicy): DraftIncidentPolicy {
  return {
    id: policy.id,
    providerId: policy.providerId,
    enabled: true,
    severity: policy.severity,
    resolveOnRecovery: true,
    dedupKeyTemplate:
      policy.dedupKeyTemplate || DEFAULT_INCIDENT_DEDUP_KEY_TEMPLATE,
    messageTemplate:
      policy.messageTemplate || DEFAULT_INCIDENT_MESSAGE_TEMPLATE,
    descriptionTemplate:
      policy.descriptionTemplate || DEFAULT_INCIDENT_DESCRIPTION_TEMPLATE,
  };
}

function policyInput(policy: DraftIncidentPolicy): IncidentPolicyInput {
  return {
    id: policy.id,
    providerId: policy.providerId,
    enabled: true,
    severity: policy.severity,
    resolveOnRecovery: true,
    dedupKeyTemplate:
      policy.dedupKeyTemplate.trim() || DEFAULT_INCIDENT_DEDUP_KEY_TEMPLATE,
    messageTemplate:
      policy.messageTemplate.trim() || DEFAULT_INCIDENT_MESSAGE_TEMPLATE,
    descriptionTemplate:
      policy.descriptionTemplate.trim() ||
      DEFAULT_INCIDENT_DESCRIPTION_TEMPLATE,
  };
}
