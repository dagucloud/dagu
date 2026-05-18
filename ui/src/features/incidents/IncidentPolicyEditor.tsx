// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  AlertTriangle,
  BellRing,
  CheckCircle2,
  Plus,
  RotateCcw,
  Trash2,
} from 'lucide-react';
import { ReactElement, ReactNode } from 'react';
import { Link } from 'react-router-dom';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { ToggleButton, ToggleGroup } from '@/components/ui/toggle-group';
import { Input } from '@/components/ui/input';
import { IncidentSeverity } from '@/api/v1/schema';
import { cn } from '@/lib/utils';
import {
  blankPolicy,
  DraftIncidentPolicy,
  DraftIncidentPolicySet,
  incidentRoutingMode,
  IncidentRoutingMode,
  INCIDENT_SEVERITIES,
  IncidentProvider,
  providerLabel,
  severityBadgeClass,
  severityLabel,
  withIncidentRoutingMode,
} from './incidentDrafts';

type IncidentPolicyEditorProps = {
  draft: DraftIncidentPolicySet;
  providers: IncidentProvider[];
  allowInherit: boolean;
  inheritTitle: string;
  inheritDescription: string;
  emptyProviderMessage?: ReactNode;
  onChange: (draft: DraftIncidentPolicySet) => void;
};

const modeLabels: Record<IncidentRoutingMode, string> = {
  inherit: 'Inherit',
  custom: 'Send incidents',
  off: 'Off',
};

export function IncidentPolicyEditor({
  draft,
  providers,
  allowInherit,
  inheritTitle,
  inheritDescription,
  emptyProviderMessage,
  onChange,
}: IncidentPolicyEditorProps): ReactElement {
  const providerById = new Map(
    providers.map((provider) => [provider.id, provider])
  );
  const enabledProviders = providers.filter((provider) => provider.enabled);
  const usedProviderIds = new Set(
    draft.policies.map((policy) => policy.providerId)
  );
  const addableProvider = enabledProviders.find(
    (provider) => provider.id && !usedProviderIds.has(provider.id)
  );
  const mode = incidentRoutingMode(draft);
  const modeOptions: IncidentRoutingMode[] = allowInherit
    ? ['inherit', 'custom', 'off']
    : ['custom', 'off'];
  const statusBadgeVariant = mode === 'custom' ? 'success' : 'default';

  const setMode = (nextMode: IncidentRoutingMode) => {
    onChange(withIncidentRoutingMode(draft, nextMode));
  };

  const updatePolicy = (
    index: number,
    updater: (policy: DraftIncidentPolicy) => DraftIncidentPolicy
  ) => {
    onChange({
      ...draft,
      enabled: true,
      inheritParent: false,
      policies: draft.policies.map((policy, policyIndex) =>
        policyIndex === index ? updater(policy) : policy
      ),
    });
  };

  const addPolicy = () => {
    if (!addableProvider) return;
    onChange({
      ...draft,
      enabled: true,
      inheritParent: false,
      policies: [...draft.policies, blankPolicy(addableProvider.id)],
    });
  };

  return (
    <div className="space-y-4">
      <div className="rounded-md border border-border bg-card p-4">
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <BellRing className="h-4 w-4 text-muted-foreground" />
              <h2 className="text-sm font-semibold text-foreground">
                {inheritTitle}
              </h2>
              <Badge variant={statusBadgeVariant}>{modeLabels[mode]}</Badge>
            </div>
            <p className="text-sm text-muted-foreground">
              {inheritDescription}
            </p>
          </div>
          <ToggleGroup aria-label="Incident routing mode">
            {modeOptions.map((option) => (
              <ToggleButton
                key={option}
                value={option}
                groupValue={mode}
                onClick={() => setMode(option)}
              >
                {modeLabels[option]}
              </ToggleButton>
            ))}
          </ToggleGroup>
        </div>
      </div>

      {mode === 'inherit' ? (
        <div className="rounded-md border border-border bg-muted/30 p-5">
          <div className="flex items-start gap-3">
            <RotateCcw className="mt-0.5 h-4 w-4 text-muted-foreground" />
            <div>
              <h3 className="text-sm font-medium text-foreground">
                Parent routing is active.
              </h3>
              <p className="mt-1 text-sm text-muted-foreground">
                This scope uses the next configured parent route.
              </p>
            </div>
          </div>
        </div>
      ) : null}

      {mode === 'off' ? (
        <div className="rounded-md border border-border bg-muted/30 p-5">
          <div className="flex items-start gap-3">
            <CheckCircle2 className="mt-0.5 h-4 w-4 text-muted-foreground" />
            <div>
              <h3 className="text-sm font-medium text-foreground">
                No new incidents from this scope.
              </h3>
              <p className="mt-1 text-sm text-muted-foreground">
                Existing open incidents still resolve when the DAG recovers.
              </p>
            </div>
          </div>
        </div>
      ) : null}

      {mode === 'custom' && providers.length === 0 ? (
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>
            {emptyProviderMessage || (
              <>
                Add an incident connection before configuring routing.{' '}
                <Link
                  to="/incident-providers"
                  className="font-medium underline underline-offset-2"
                >
                  Open connections
                </Link>
              </>
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      {mode === 'custom' && providers.length > 0 ? (
        <div className="space-y-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 className="text-sm font-semibold text-foreground">
                Send Incidents To
              </h2>
              <p className="text-sm text-muted-foreground">
                Final failures open incidents. Later successful runs resolve
                them.
              </p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={addPolicy}
              disabled={!addableProvider}
            >
              <Plus className="h-4 w-4" />
              Add connection
            </Button>
          </div>

          {draft.policies.length === 0 ? (
            <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
              No incident connections selected.
            </div>
          ) : null}

          <div className="space-y-3">
            {draft.policies.map((policy, index) => {
              const provider = providerById.get(policy.providerId);
              return (
                <div
                  key={policy.id || `${policy.providerId}-${index}`}
                  className="rounded-md border border-border bg-card p-4"
                >
                  <div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                    <div className="min-w-0 space-y-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-semibold text-foreground">
                          {provider?.name || 'Missing connection'}
                        </span>
                        <Badge
                          className={cn(
                            'border-border',
                            severityBadgeClass(policy.severity)
                          )}
                        >
                          {severityLabel(policy.severity)}
                        </Badge>
                        {provider && !provider.enabled ? (
                          <Badge variant="default">Connection disabled</Badge>
                        ) : null}
                      </div>
                      <p className="text-sm text-muted-foreground">
                        Opens on final failure and resolves on recovery.
                      </p>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-destructive hover:text-destructive"
                      onClick={() =>
                        onChange({
                          ...draft,
                          policies: draft.policies.filter(
                            (_, policyIndex) => policyIndex !== index
                          ),
                        })
                      }
                      aria-label="Remove incident connection"
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>

                  <div className="mt-4 grid gap-4 lg:grid-cols-2">
                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Connection
                      </label>
                      <Select
                        value={policy.providerId}
                        onValueChange={(providerId) =>
                          updatePolicy(index, (current) => ({
                            ...current,
                            providerId,
                          }))
                        }
                      >
                        <SelectTrigger className="h-7">
                          <SelectValue placeholder="Select connection" />
                        </SelectTrigger>
                        <SelectContent>
                          {providers.map((candidate) => {
                            const disabled =
                              !candidate.enabled ||
                              (candidate.id !== policy.providerId &&
                                usedProviderIds.has(candidate.id));
                            return (
                              <SelectItem
                                key={candidate.id}
                                value={candidate.id}
                                disabled={disabled}
                              >
                                {candidate.name} ·{' '}
                                {providerLabel(candidate.type)}
                              </SelectItem>
                            );
                          })}
                        </SelectContent>
                      </Select>
                    </div>

                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Severity
                      </label>
                      <Select
                        value={policy.severity}
                        onValueChange={(severity) =>
                          updatePolicy(index, (current) => ({
                            ...current,
                            severity: severity as IncidentSeverity,
                          }))
                        }
                      >
                        <SelectTrigger className="h-7">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {INCIDENT_SEVERITIES.map((severity) => (
                            <SelectItem key={severity} value={severity}>
                              {severityLabel(severity)}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  </div>

                  <div className="mt-4 grid gap-4">
                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Message
                      </label>
                      <Input
                        value={policy.messageTemplate}
                        onChange={(event) =>
                          updatePolicy(index, (current) => ({
                            ...current,
                            messageTemplate: event.target.value,
                          }))
                        }
                      />
                    </div>

                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Details
                      </label>
                      <Textarea
                        className="min-h-28 resize-y"
                        value={policy.descriptionTemplate}
                        onChange={(event) =>
                          updatePolicy(index, (current) => ({
                            ...current,
                            descriptionTemplate: event.target.value,
                          }))
                        }
                      />
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      ) : null}
    </div>
  );
}
