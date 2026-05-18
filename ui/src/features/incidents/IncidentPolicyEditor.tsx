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
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import { IncidentSeverity } from '@/api/v1/schema';
import { cn } from '@/lib/utils';
import {
  blankPolicy,
  DraftIncidentPolicy,
  DraftIncidentPolicySet,
  INCIDENT_SEVERITIES,
  IncidentProvider,
  providerLabel,
  severityBadgeClass,
  severityLabel,
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
  const usedProviderIds = new Set(
    draft.policies.map((policy) => policy.providerId)
  );
  const addableProvider = providers.find(
    (provider) => provider.id && !usedProviderIds.has(provider.id)
  );
  const inheritsParent = allowInherit && draft.inheritParent;
  let statusBadgeLabel = 'Disabled';
  if (inheritsParent) {
    statusBadgeLabel = 'Inherits';
  } else if (draft.enabled) {
    statusBadgeLabel = 'Enabled';
  }
  const statusBadgeVariant =
    !inheritsParent && draft.enabled ? 'success' : 'default';

  const updatePolicy = (
    index: number,
    updater: (policy: DraftIncidentPolicy) => DraftIncidentPolicy
  ) => {
    onChange({
      ...draft,
      policies: draft.policies.map((policy, policyIndex) =>
        policyIndex === index ? updater(policy) : policy
      ),
    });
  };

  const addPolicy = () => {
    if (!addableProvider) return;
    onChange({
      ...draft,
      inheritParent: false,
      policies: [...draft.policies, blankPolicy(addableProvider.id)],
    });
  };

  return (
    <div className="space-y-4">
      <div className="rounded-md border border-border bg-card p-4">
        <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
          <div className="space-y-1">
            <div className="flex items-center gap-2">
              <BellRing className="h-4 w-4 text-muted-foreground" />
              <h2 className="text-sm font-semibold text-foreground">
                {inheritTitle}
              </h2>
              <Badge variant={statusBadgeVariant}>{statusBadgeLabel}</Badge>
            </div>
            <p className="text-sm text-muted-foreground">
              {inheritDescription}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-4">
            {allowInherit && (
              <label className="flex items-center gap-2 text-sm text-foreground">
                <Switch
                  checked={draft.inheritParent}
                  onCheckedChange={(checked) =>
                    onChange({ ...draft, inheritParent: checked })
                  }
                />
                Inherit parent
              </label>
            )}
            {!inheritsParent && (
              <label className="flex items-center gap-2 text-sm text-foreground">
                <Switch
                  checked={draft.enabled}
                  onCheckedChange={(checked) =>
                    onChange({ ...draft, enabled: checked })
                  }
                />
                Enabled
              </label>
            )}
          </div>
        </div>
      </div>

      {inheritsParent ? (
        <div className="rounded-md border border-border bg-muted/30 p-5">
          <div className="flex items-start gap-3">
            <RotateCcw className="mt-0.5 h-4 w-4 text-muted-foreground" />
            <div>
              <h3 className="text-sm font-medium text-foreground">
                This scope inherits its parent policy.
              </h3>
              <p className="mt-1 text-sm text-muted-foreground">
                Turn off inherit parent only when this scope needs a different
                incident provider, severity, or message template.
              </p>
            </div>
          </div>
        </div>
      ) : null}

      {!inheritsParent && providers.length === 0 ? (
        <Alert variant="warning">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>
            {emptyProviderMessage || (
              <>
                Add an incident provider before configuring policies.{' '}
                <Link
                  to="/incident-providers"
                  className="font-medium underline underline-offset-2"
                >
                  Open providers
                </Link>
              </>
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      {!inheritsParent && providers.length > 0 ? (
        <div className="space-y-3">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 className="text-sm font-semibold text-foreground">
                Incident Policies
              </h2>
              <p className="text-sm text-muted-foreground">
                Each provider can appear once in this scope.
              </p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={addPolicy}
              disabled={!addableProvider}
            >
              <Plus className="h-4 w-4" />
              Add policy
            </Button>
          </div>

          {draft.policies.length === 0 ? (
            <div className="rounded-md border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
              No incident policies configured for this scope.
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
                          {provider?.name || 'Missing provider'}
                        </span>
                        <Badge variant={policy.enabled ? 'success' : 'default'}>
                          {policy.enabled ? 'Enabled' : 'Disabled'}
                        </Badge>
                        <Badge
                          className={cn(
                            'border-border',
                            severityBadgeClass(policy.severity)
                          )}
                        >
                          {severityLabel(policy.severity)}
                        </Badge>
                      </div>
                      <p className="text-sm text-muted-foreground">
                        Opens on final DAG failure after retries are exhausted.
                      </p>
                    </div>
                    <div className="flex flex-wrap items-center gap-3">
                      <label className="flex items-center gap-2 text-sm text-foreground">
                        <Switch
                          checked={policy.enabled}
                          onCheckedChange={(checked) =>
                            updatePolicy(index, (current) => ({
                              ...current,
                              enabled: checked,
                            }))
                          }
                        />
                        Enabled
                      </label>
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
                        aria-label="Delete incident policy"
                      >
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    </div>
                  </div>

                  <div className="mt-4 grid gap-4 lg:grid-cols-2">
                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Send to
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
                        <SelectTrigger>
                          <SelectValue placeholder="Select provider" />
                        </SelectTrigger>
                        <SelectContent>
                          {providers.map((candidate) => {
                            const disabled =
                              candidate.id !== policy.providerId &&
                              usedProviderIds.has(candidate.id);
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
                        <SelectTrigger>
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

                  <div className="mt-4 flex flex-wrap items-center gap-4">
                    <label className="flex items-center gap-2 text-sm text-foreground">
                      <Checkbox
                        checked={policy.resolveOnRecovery}
                        onCheckedChange={(checked) =>
                          updatePolicy(index, (current) => ({
                            ...current,
                            resolveOnRecovery: checked === true,
                          }))
                        }
                      />
                      Resolve when DAG later succeeds
                    </label>
                  </div>

                  <div className="mt-4 grid gap-4">
                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Dedup key template
                      </label>
                      <Input
                        value={policy.dedupKeyTemplate}
                        onChange={(event) =>
                          updatePolicy(index, (current) => ({
                            ...current,
                            dedupKeyTemplate: event.target.value,
                          }))
                        }
                      />
                      <p className="text-xs leading-5 text-muted-foreground">
                        Keep this stable so a later success can resolve the same
                        incident.
                      </p>
                    </div>

                    <div className="space-y-2">
                      <label className="text-xs font-medium text-muted-foreground">
                        Message template
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
                        Description template
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
