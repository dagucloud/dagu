// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { AlertTriangle, Plus, Trash2 } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';
import { withWorkspaceLabel, withoutWorkspaceLabels } from '@/lib/workspace';
import type { ControllerDAGOption } from '../dagOptions';
import {
  CONTROLLER_LLM_PROVIDER_OPTIONS,
  CONTROLLER_STATE_NAME_PATTERN,
  MAX_CONTROLLER_MAX_TURNS,
  MIN_CONTROLLER_MAX_TURNS,
} from '../constraints';
import { ROUTER_INSTRUCTION, systemSuffix, withSystemSuffix } from '../draft';
import type { ControllerDefinition, ControllerState } from '../types';

type Props = {
  definition: ControllerDefinition;
  workspace: string;
  availableDAGs?: ControllerDAGOption[];
  availableDAGsError?: string;
  availableDAGsLoading?: boolean;
  onRetryAvailableDAGs?: () => void;
  readOnly?: boolean;
  onDraftDirtyChange?: (dirty: boolean) => void;
  onChange: (definition: ControllerDefinition) => void;
};

function cloneDefinition(value: ControllerDefinition): ControllerDefinition {
  return {
    ...value,
    labels: [...value.labels],
    dags: [...value.dags],
    llm: { ...value.llm },
    states: Object.fromEntries(
      Object.entries(value.states).map(([name, state]) => [
        name,
        {
          ...state,
          dags: [...state.dags],
          transitions: state.transitions.map((transition) => ({
            ...transition,
          })),
        },
      ])
    ),
  };
}

function parseList(value: string): string[] {
  return [
    ...new Set(
      value
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean)
    ),
  ];
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      {children}
    </div>
  );
}

function StateCard({
  name,
  state,
  stateNames,
  controllerDAGs,
  readOnly,
  onDraftDirtyChange,
  onRename,
  onRemove,
  onChange,
}: {
  name: string;
  state: ControllerState;
  stateNames: string[];
  controllerDAGs: string[];
  readOnly: boolean;
  onDraftDirtyChange: (dirty: boolean) => void;
  onRename: (nextName: string) => boolean;
  onRemove: () => void;
  onChange: (state: ControllerState) => void;
}) {
  const serializedStateDAGs = state.dags.join(', ');
  const [nameDraft, setNameDraft] = React.useState(name);
  const [dagListDraft, setDAGListDraft] = React.useState(serializedStateDAGs);

  React.useEffect(
    () => setDAGListDraft(serializedStateDAGs),
    [serializedStateDAGs]
  );

  const update = (patch: Partial<ControllerState>) =>
    onChange({ ...state, ...patch });

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <div className="min-w-0 flex-1">
          {name === 'default' ? (
            <div className="flex items-center gap-2">
              <CardTitle>default</CardTitle>
              <Badge variant="primary">Initial</Badge>
            </div>
          ) : (
            <Input
              aria-label={`State name ${name}`}
              value={nameDraft}
              disabled={readOnly}
              onChange={(event) => {
                setNameDraft(event.target.value);
                onDraftDirtyChange(event.target.value !== name);
              }}
              onBlur={() => {
                if (!onRename(nameDraft)) setNameDraft(name);
                onDraftDirtyChange(false);
              }}
              className="max-w-xs font-semibold"
            />
          )}
        </div>
        {name !== 'default' && (
          <Button
            variant="ghost"
            size="icon-sm"
            disabled={readOnly}
            onClick={onRemove}
            aria-label={`Delete state ${name}`}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        <Field label="Description">
          <Textarea
            value={state.description ?? ''}
            disabled={readOnly}
            onChange={(event) => update({ description: event.target.value })}
            className="min-h-20"
          />
        </Field>
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="Terminal outcome">
            <select
              value={state.terminal ?? ''}
              disabled={readOnly}
              onChange={(event) =>
                update({
                  terminal:
                    event.target.value === 'succeeded' ||
                    event.target.value === 'failed'
                      ? event.target.value
                      : undefined,
                })
              }
              className="h-9 w-full rounded-md border border-input bg-card px-3 text-sm"
            >
              <option value="">Non-terminal</option>
              <option value="succeeded">Succeeded</option>
              <option value="failed">Failed</option>
            </select>
          </Field>
          <Field label="Callable DAGs">
            <Input
              aria-label={`Callable DAGs for ${name}`}
              value={dagListDraft}
              disabled={readOnly || Boolean(state.terminal)}
              placeholder="triage, notify"
              onChange={(event) => {
                setDAGListDraft(event.target.value);
                onDraftDirtyChange(event.target.value !== serializedStateDAGs);
              }}
              onBlur={() => {
                const nextDAGs = parseList(dagListDraft).filter((dag) =>
                  controllerDAGs.includes(dag)
                );
                setDAGListDraft(nextDAGs.join(', '));
                update({ dags: nextDAGs });
                onDraftDirtyChange(false);
              }}
            />
          </Field>
        </div>
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <Label>Transitions</Label>
            <Button
              variant="secondary"
              size="xs"
              disabled={readOnly || Boolean(state.terminal)}
              onClick={() =>
                update({
                  transitions: [
                    ...state.transitions,
                    {
                      to:
                        stateNames.find((candidate) => candidate !== name) ??
                        name,
                      when: '',
                    },
                  ],
                })
              }
            >
              <Plus className="h-3.5 w-3.5" />
              Transition
            </Button>
          </div>
          {state.transitions.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              No outgoing transitions.
            </p>
          ) : (
            state.transitions.map((transition, index) => (
              <div
                key={`${name}-${index}`}
                className="grid gap-2 rounded-md border border-border p-2 md:grid-cols-[minmax(9rem,0.35fr)_1fr_auto]"
              >
                <select
                  aria-label={`Transition ${index + 1} destination`}
                  value={transition.to}
                  disabled={readOnly}
                  onChange={(event) => {
                    const transitions = state.transitions.map(
                      (item, itemIndex) =>
                        itemIndex === index
                          ? { ...item, to: event.target.value }
                          : item
                    );
                    update({ transitions });
                  }}
                  className="h-9 rounded-md border border-input bg-card px-3 text-sm"
                >
                  {stateNames.map((stateName) => (
                    <option key={stateName} value={stateName}>
                      {stateName}
                    </option>
                  ))}
                </select>
                <Input
                  aria-label={`Transition ${index + 1} condition`}
                  value={transition.when}
                  disabled={readOnly}
                  placeholder="When the incident has been classified"
                  onChange={(event) => {
                    const transitions = state.transitions.map(
                      (item, itemIndex) =>
                        itemIndex === index
                          ? { ...item, when: event.target.value }
                          : item
                    );
                    update({ transitions });
                  }}
                />
                <Button
                  variant="ghost"
                  size="icon-sm"
                  disabled={readOnly}
                  onClick={() =>
                    update({
                      transitions: state.transitions.filter(
                        (_, itemIndex) => itemIndex !== index
                      ),
                    })
                  }
                  aria-label={`Delete transition ${index + 1}`}
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))
          )}
        </div>
      </CardContent>
    </Card>
  );
}

export function ControllerBuilder({
  definition,
  workspace,
  availableDAGs = [],
  availableDAGsError,
  availableDAGsLoading = false,
  onRetryAvailableDAGs,
  readOnly = false,
  onDraftDirtyChange = () => {},
  onChange,
}: Props) {
  const [builderMessage, setBuilderMessage] = React.useState<string | null>(
    null
  );
  const [dagSearch, setDAGSearch] = React.useState('');
  const serializedLabels = withoutWorkspaceLabels(definition.labels).join(', ');
  const [labelListDraft, setLabelListDraft] = React.useState(serializedLabels);
  const serializedDAGs = definition.dags.join(', ');
  const [dagListDraft, setDAGListDraft] = React.useState(serializedDAGs);
  const suffix = systemSuffix(definition.llm.system);
  const builderReadOnly = readOnly || suffix === null;

  React.useEffect(() => {
    setDAGListDraft(serializedDAGs);
  }, [serializedDAGs]);

  React.useEffect(() => {
    setLabelListDraft(serializedLabels);
  }, [serializedLabels]);

  const commit = React.useCallback(
    (mutate: (draft: ControllerDefinition) => void) => {
      const draft = cloneDefinition(definition);
      mutate(draft);
      onChange(draft);
      setBuilderMessage(null);
    },
    [definition, onChange]
  );

  const renameState = (name: string, nextName: string) => {
    if (nextName === name) return true;
    if (!CONTROLLER_STATE_NAME_PATTERN.test(nextName)) {
      setBuilderMessage(
        'State names must start with a letter and use letters, numbers, _ or -.'
      );
      return false;
    }
    if (Object.prototype.hasOwnProperty.call(definition.states, nextName)) {
      setBuilderMessage(`State ${nextName} already exists.`);
      return false;
    }
    commit((draft) => {
      const renamed = draft.states[name];
      if (!renamed) return;
      draft.states = Object.fromEntries(
        Object.entries(draft.states).map(([stateName, state]) => [
          stateName === name ? nextName : stateName,
          {
            ...state,
            transitions: state.transitions.map((transition) => ({
              ...transition,
              to: transition.to === name ? nextName : transition.to,
            })),
          },
        ])
      );
    });
    return true;
  };

  const removeState = (name: string) => {
    const state = definition.states[name];
    if (state && (state.dags.length > 0 || state.transitions.length > 0)) {
      setBuilderMessage(
        `Clear DAGs and transitions from ${name} before deleting the state.`
      );
      return;
    }
    const inbound = Object.entries(definition.states).find(([, state]) =>
      state.transitions.some((transition) => transition.to === name)
    );
    if (inbound) {
      setBuilderMessage(
        `Remove transitions to ${name} before deleting the state.`
      );
      return;
    }
    commit((draft) => {
      delete draft.states[name];
    });
  };

  const updateControllerDAGs = (nextDAGs: string[]) => {
    const referenced = new Set(
      Object.values(definition.states).flatMap((state) => state.dags)
    );
    const removedReference = [...referenced].find(
      (dag) => !nextDAGs.includes(dag)
    );
    if (removedReference) {
      setBuilderMessage(
        `Remove ${removedReference} from every state before removing it from the allowlist.`
      );
      return false;
    }
    commit((draft) => {
      draft.dags = nextDAGs;
    });
    return true;
  };

  const visibleDAGs = React.useMemo(() => {
    const term = dagSearch.toLocaleLowerCase();
    return availableDAGs.filter(
      (dag) =>
        !term ||
        dag.fileName.toLocaleLowerCase().includes(term) ||
        dag.name.toLocaleLowerCase().includes(term) ||
        dag.description?.toLocaleLowerCase().includes(term)
    );
  }, [availableDAGs, dagSearch]);

  return (
    <div className="space-y-5">
      {suffix === null && (
        <Alert variant="warning">
          <AlertDescription>
            The raw system prompt cannot be represented safely in Builder. Fix
            it in YAML first.
          </AlertDescription>
        </Alert>
      )}
      {builderMessage && (
        <Alert variant="warning">
          <AlertDescription>{builderMessage}</AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Basics</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 md:grid-cols-2">
          {definition.id && (
            <Field label="Controller ID">
              <Input value={definition.id} readOnly />
            </Field>
          )}
          <Field label="Name">
            <Input
              value={definition.name}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.name = event.target.value;
                })
              }
            />
          </Field>
          <Field label="Description">
            <Textarea
              value={definition.description ?? ''}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.description = event.target.value;
                })
              }
            />
          </Field>
          <Field label="Maximum turns">
            <Input
              type="number"
              min={MIN_CONTROLLER_MAX_TURNS}
              max={MAX_CONTROLLER_MAX_TURNS}
              value={definition.maxTurns}
              disabled={builderReadOnly}
              onChange={(event) =>
                commit((draft) => {
                  draft.maxTurns = Number.parseInt(event.target.value, 10) || 0;
                })
              }
            />
          </Field>
          <Field label="Workspace">
            <Input value={workspace || 'default'} readOnly />
          </Field>
          <Field label="Labels (comma separated)">
            <Input
              aria-label="Controller labels"
              value={labelListDraft}
              disabled={builderReadOnly}
              onChange={(event) => {
                setLabelListDraft(event.target.value);
                onDraftDirtyChange(event.target.value !== serializedLabels);
              }}
              onBlur={() => {
                const labels = parseList(labelListDraft);
                setLabelListDraft(labels.join(', '));
                commit((draft) => {
                  draft.labels = withWorkspaceLabel(labels, workspace);
                });
                onDraftDirtyChange(false);
              }}
            />
          </Field>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Router model</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <Field label="Provider">
              <select
                value={definition.llm.provider}
                disabled={builderReadOnly}
                onChange={(event) =>
                  commit((draft) => {
                    draft.llm.provider = event.target.value;
                  })
                }
                className="h-9 w-full rounded-md border border-input bg-card px-3 text-sm"
              >
                {CONTROLLER_LLM_PROVIDER_OPTIONS.map(({ value, label }) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Model">
              <Input
                value={definition.llm.model}
                disabled={builderReadOnly}
                placeholder="Provider model ID"
                onChange={(event) =>
                  commit((draft) => {
                    draft.llm.model = event.target.value;
                  })
                }
              />
            </Field>
          </div>
          <Field label="Reserved Router instruction">
            <Input value={ROUTER_INSTRUCTION} readOnly />
          </Field>
          <Field label="Additional system instructions">
            <Textarea
              value={suffix ?? ''}
              disabled={builderReadOnly}
              placeholder="Optional instructions appended after the reserved Router instruction."
              onChange={(event) =>
                commit((draft) => {
                  draft.llm.system = withSystemSuffix(event.target.value);
                })
              }
              className="min-h-28"
            />
          </Field>
          <Alert variant="warning">
            <AlertTriangle className="h-4 w-4" />
            <AlertDescription>
              These instructions are stored and may be sent to an external LLM.
              Do not include secrets.
            </AlertDescription>
          </Alert>
          <p className="text-xs text-muted-foreground">
            The reserved placeholder is always first and cannot be edited in
            Builder.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Controller DAG allowlist</CardTitle>
        </CardHeader>
        <CardContent>
          {availableDAGsError && (
            <Alert variant="destructive" className="mb-4">
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>{availableDAGsError}</span>
                {onRetryAvailableDAGs && (
                  <Button
                    type="button"
                    size="xs"
                    variant="outline"
                    disabled={availableDAGsLoading}
                    onClick={onRetryAvailableDAGs}
                  >
                    Retry
                  </Button>
                )}
              </AlertDescription>
            </Alert>
          )}
          {availableDAGsLoading && (
            <p className="mb-3 text-xs text-muted-foreground">
              Loading compatible DAGs…
            </p>
          )}
          <Field label="DAG names (comma separated)">
            <Input
              aria-label="Controller DAG allowlist"
              value={dagListDraft}
              disabled={builderReadOnly}
              placeholder="triage, notify"
              onChange={(event) => {
                setDAGListDraft(event.target.value);
                onDraftDirtyChange(event.target.value !== serializedDAGs);
              }}
              onBlur={() => {
                const requested = parseList(dagListDraft);
                if (updateControllerDAGs(requested)) {
                  setDAGListDraft(requested.join(', '));
                } else {
                  setDAGListDraft(serializedDAGs);
                }
                onDraftDirtyChange(false);
              }}
            />
          </Field>
          {availableDAGs.length > 0 && (
            <div className="mt-4 space-y-2">
              <Input
                value={dagSearch}
                onChange={(event) => setDAGSearch(event.target.value)}
                placeholder="Search DAGs in this workspace…"
                disabled={builderReadOnly}
              />
              <div className="max-h-64 divide-y divide-border overflow-auto rounded-md border border-border">
                {visibleDAGs.map((dag) => {
                  const selected = definition.dags.includes(dag.fileName);
                  return (
                    <label
                      key={dag.fileName}
                      className="flex cursor-pointer items-start gap-3 p-3 hover:bg-muted/50"
                    >
                      <input
                        type="checkbox"
                        checked={selected}
                        disabled={builderReadOnly}
                        onChange={() =>
                          updateControllerDAGs(
                            selected
                              ? definition.dags.filter(
                                  (candidate) => candidate !== dag.fileName
                                )
                              : [...definition.dags, dag.fileName]
                          )
                        }
                        className="mt-1"
                      />
                      <span className="min-w-0">
                        <span className="block font-mono text-sm font-medium">
                          {dag.fileName}
                        </span>
                        {dag.description && (
                          <span className="block truncate text-xs text-muted-foreground">
                            {dag.description}
                          </span>
                        )}
                      </span>
                    </label>
                  );
                })}
                {visibleDAGs.length === 0 && (
                  <p className="p-4 text-center text-xs text-muted-foreground">
                    No DAGs match this search.
                  </p>
                )}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold">States and transitions</h3>
          <p className="text-sm text-muted-foreground">
            Every execution starts in default.
          </p>
        </div>
        <Button
          variant="secondary"
          disabled={builderReadOnly}
          onClick={() => {
            let index = Object.keys(definition.states).length + 1;
            let name = `state${index}`;
            while (definition.states[name]) name = `state${++index}`;
            commit((draft) => {
              draft.states[name] = {
                description: '',
                dags: [],
                transitions: [],
              };
            });
          }}
        >
          <Plus className="h-4 w-4" />
          Add state
        </Button>
      </div>
      {Object.entries(definition.states).map(([name, state]) => (
        <StateCard
          key={name}
          name={name}
          state={state}
          stateNames={Object.keys(definition.states)}
          controllerDAGs={definition.dags}
          readOnly={builderReadOnly}
          onDraftDirtyChange={onDraftDirtyChange}
          onRename={(nextName) => renameState(name, nextName)}
          onRemove={() => removeState(name)}
          onChange={(nextState) => {
            if (
              nextState.terminal &&
              (state.dags.length > 0 || state.transitions.length > 0)
            ) {
              setBuilderMessage(
                `Clear DAGs and transitions from ${name} before making it terminal.`
              );
              return;
            }
            commit((draft) => {
              draft.states[name] = nextState;
            });
          }}
        />
      ))}
    </div>
  );
}
