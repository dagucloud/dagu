// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { KeyRound, Trash2 } from 'lucide-react';
import { useMemo, type ReactElement } from 'react';
import { IconButton } from '@/components/ui/icon-button';
import { Switch } from '@/components/ui/switch';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import type { ReferenceCandidate } from '../../contracts';
import {
  runsProcess,
  trustOf,
  type TrustSource,
} from '../../agentRun/agentRun';
import { UntrustedBadge } from '../../agentRun/UntrustedBadge';
import { FieldIssues } from '../FieldIssues';
import { issuesUnder } from '../issues';
import { Section } from '../Section';
import type { SectionProps } from '../types';
import { YamlOnlyValue } from './chat/fields';
import { SELECT_CLASS } from './RunEditor';

/** The step keys of this section, for the field paths of the issues. */
export const SECURITY_FIELDS = ['secrets', 'untrusted'] as const;

/** The names of the secrets that the DAG declares, from the reference candidates. */
export function declaredSecrets(candidates: ReferenceCandidate[]): string[] {
  return candidates
    .filter((candidate) => candidate.group === 'secrets')
    .map((candidate) => candidate.label);
}

/** The names of `secrets` when it is a list of text, else null. */
export function readSecretList(value: unknown): string[] | null {
  if (value === undefined) return [];
  return Array.isArray(value) && value.every((item) => typeof item === 'string')
    ? (value as string[])
    : null;
}

/** The note under the switch, from where the trust of the step comes. */
function trustNote(source: TrustSource): string {
  switch (source) {
    case 'default':
      return 'Untrusted by default: the DAG has an agent step.';
    case 'opted-out':
      return 'Trusted: the key untrusted is false.';
    default:
      return 'The step gets no secret and no DAGU_ variable.';
  }
}

export interface SecuritySectionProps extends SectionProps {
  /** The reference candidates of the step: the secrets of the DAG are in them. */
  candidates: ReferenceCandidate[];
  /** The number of issues of the section. */
  count: number;
  revealKey?: number;
  /**
   * True when the DAG has an agent step. A step that runs a process is then
   * untrusted by default, and the section writes `untrusted: false` to make it
   * trusted.
   */
  agentDag?: boolean;
}

/**
 * The keys `untrusted` and `secrets` of a step (Spec 092). An agent step
 * runs a CLI and is always untrusted: the section shows the mark and no
 * switch. For another step the switch shows the effective value: in a DAG
 * that has an agent step, a step that runs a process is untrusted by default,
 * and the switch writes `untrusted: false` to make it trusted. An untrusted
 * step takes no secret, so it has no list of secrets. A control that offers
 * no choice is not drawn, and the whole section goes when it has none.
 */
export function SecuritySection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  candidates,
  count,
  revealKey,
  agentDag = false,
}: SecuritySectionProps): ReactElement | null {
  const { ts } = useI18n();
  const declared = useMemo(() => declaredSecrets(candidates), [candidates]);
  const trust = trustOf(step, agentDag);
  const cli = trust.source === 'agent';
  const untrusted = trust.untrusted;
  const hasSecrets = 'secrets' in step.value;
  const showUntrusted = cli || runsProcess(step) || 'untrusted' in step.value;
  const showSecrets = hasSecrets || (!untrusted && declared.length > 0);
  if (!showUntrusted && !showSecrets) return null;
  const set = (key: string, value: unknown) =>
    onEdit({ kind: 'setStepField', step: step.key, path: [key], value });

  return (
    <Section
      title={<I18nText text="Security" />}
      count={count}
      revealKey={revealKey}
    >
      <div className="space-y-3">
        {showUntrusted && (
          <div className="space-y-1" data-field-path="untrusted">
            {cli ? (
              <div className="flex items-center gap-2 text-xs">
                {step.value.untrusted === true && <UntrustedBadge />}
                <span className="text-muted-foreground">
                  <I18nText text="A step with a CLI provider is always untrusted." />
                </span>
              </div>
            ) : (
              <label className="flex items-center justify-between gap-2 text-xs">
                <I18nText text="Untrusted" />
                <Switch
                  aria-label={ts('Untrusted')}
                  checked={untrusted}
                  disabled={readOnly}
                  onCheckedChange={(checked) =>
                    // Off is an explicit false in a DAG that has an agent step,
                    // where untrusted is the default; on is then the default.
                    set(
                      'untrusted',
                      agentDag
                        ? checked
                          ? undefined
                          : false
                        : checked || undefined
                    )
                  }
                />
              </label>
            )}
            {!cli && (
              <p className="text-[11px] text-muted-foreground">
                <I18nText text={trustNote(trust.source)} />
              </p>
            )}
            {/* The key is redundant on this step, and false is an error: the YAML removes it. */}
            {cli && 'untrusted' in step.value && (
              <YamlOnlyValue name="untrusted" value={step.value.untrusted} />
            )}
            <FieldIssues issues={fieldIssues.untrusted} />
          </div>
        )}
        {showSecrets && (
          <SecretList
            value={step.value.secrets}
            declared={declared}
            readOnly={readOnly}
            issues={issuesUnder(fieldIssues, 'secrets')}
            onSet={(names) => set('secrets', names)}
          />
        )}
      </div>
    </Section>
  );
}

/**
 * The list `secrets` of a step: names of the secrets of the DAG. Each row
 * has a remove button, and a select adds a declared secret that the list does
 * not have yet. A value that is not a list of names stays in the YAML.
 */
function SecretList({
  value,
  declared,
  readOnly,
  issues,
  onSet,
}: {
  value: unknown;
  declared: string[];
  readOnly: boolean;
  issues: SectionProps['fieldIssues'][string];
  onSet(names: string[] | undefined): void;
}): ReactElement {
  const { ts } = useI18n();
  const names = readSecretList(value);
  if (names === null) {
    return (
      <div className="space-y-1" data-field-path="secrets">
        <YamlOnlyValue name="secrets" value={value} />
        <FieldIssues issues={issues} />
      </div>
    );
  }
  const addable = declared.filter((name) => !names.includes(name));
  return (
    <div className="space-y-1" data-field-path="secrets">
      <div className="text-xs font-medium text-muted-foreground">
        <I18nText text="Secrets" />
      </div>
      {names.length === 0 && (
        <p className="text-xs text-muted-foreground">
          <I18nText text="No secret yet" />
        </p>
      )}
      <ul className="space-y-1">
        {names.map((name, index) => (
          <li
            key={`${index}-${name}`}
            data-testid="ve-secret-entry"
            data-field-path={`secrets[${index}]`}
            className="flex items-center gap-1 rounded border border-border px-1 py-0.5"
          >
            <KeyRound
              className="ml-1 size-3.5 shrink-0 text-muted-foreground"
              aria-hidden
            />
            <span className="min-w-0 flex-1 truncate font-mono text-xs">
              {name}
            </span>
            {!readOnly && (
              <IconButton
                size="sm"
                tone="destructive"
                label={ts('Remove secret {name}', { name })}
                icon={Trash2}
                onClick={() => {
                  const next = names.filter((_, at) => at !== index);
                  onSet(next.length > 0 ? next : undefined);
                }}
              />
            )}
          </li>
        ))}
      </ul>
      <FieldIssues issues={issues} />
      {!readOnly && addable.length > 0 && (
        <select
          data-testid="ve-secret-add"
          className={SELECT_CLASS}
          aria-label={ts('Add a secret')}
          value=""
          onChange={(event) => {
            if (event.target.value === '') return;
            onSet([...names, event.target.value]);
          }}
        >
          <option value="">{ts('Add a secret')}</option>
          {addable.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      )}
      <p className="text-[11px] text-muted-foreground">
        <I18nText text="Once a step lists secrets, each step gets only the secrets that it lists." />
      </p>
    </div>
  );
}
