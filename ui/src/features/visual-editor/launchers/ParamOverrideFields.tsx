// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RotateCcw } from 'lucide-react';
import { useId, type ReactElement } from 'react';
import {
  ParamDefType,
  ValidationIssueSeverity,
  ValidationIssueSource,
  type components,
} from '@/api/v1/schema';
import { IconButton } from '@/components/ui/icon-button';
import { validateParamValue } from '@/features/dags/components/dag-execution/paramValue';
import { useI18n } from '@/i18n/I18nProvider';
import { FieldIssues } from '../inspector/FieldIssues';
import { TextField } from '../inspector/TextField';

type ParamDef = components['schemas']['ParamDef'];
type ParamScalar = components['schemas']['ParamScalar'];

/** An override as the YAML writes it. */
export type OverrideValue = string | number | boolean;

/** Placeholder of an override set to the empty string, which a blank field hides. */
const EMPTY_OVERRIDE = '""';

/**
 * The option of a select that clears the override. The other options are
 * named by their position, so that no value can be taken for this one.
 */
const DEFAULT_OPTION = '';

/** A variable or a command substitution, resolved when the run starts. */
const DYNAMIC_VALUE = /\$\{|`/;

const SELECT_CLASS =
  'h-7 w-full min-w-0 rounded-md border border-input bg-background px-2 text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50';

function scalarText(value: ParamScalar): string {
  return typeof value === 'string' ? value : String(value);
}

/**
 * The significant digits of a decimal number and the position of its point,
 * or undefined for text that is not a decimal number. Two spellings of one
 * value, such as 1.50 and 1.5, give the same result.
 */
function decimalValue(text: string): string | undefined {
  const match = /^(-?)(\d+\.?\d*|\.\d+)(?:[eE]([-+]?\d+))?$/.exec(text);
  if (!match) return undefined;
  const [whole = '', fraction = ''] = (match[2] ?? '').split('.');
  const all = whole + fraction;
  const significant = all.replace(/^0+/, '');
  const digits = significant.replace(/0+$/, '');
  if (digits === '') return '0';
  const point =
    whole.length + Number(match[3] ?? 0) - (all.length - significant.length);
  return `${match[1]}${digits}e${point}`;
}

/**
 * The value an override takes in the YAML: a number or boolean for a param
 * of that type, and the text otherwise. A number is written only when it
 * keeps the value of the text, so that no digit is lost; a spelling that
 * is not decimal, such as 0x10, stays text for the backend to read.
 */
function typedValue(def: ParamDef | undefined, text: string): OverrideValue {
  if (!def || DYNAMIC_VALUE.test(text)) return text;
  switch (def.type) {
    case ParamDefType.integer:
    case ParamDefType.number: {
      const trimmed = text.trim();
      const number = Number(trimmed);
      const value = decimalValue(trimmed);
      return value !== undefined && value === decimalValue(String(number))
        ? number
        : text;
    }
    case ParamDefType.boolean:
      if (text === 'true') return true;
      if (text === 'false') return false;
      return text;
    default:
      return text;
  }
}

/** The values a param can take from a list, or undefined for free text. */
function choicesOf(def: ParamDef | undefined): string[] | undefined {
  if (def?.enum?.length) return def.enum.map(scalarText);
  if (def?.type === ParamDefType.boolean) return ['true', 'false'];
  return undefined;
}

/**
 * One field per workflow parameter, and one per override of a name the
 * workflow does not declare. A param with a list of values, or a boolean,
 * takes a select whose first option is the workflow default. Other params
 * take text, checked against their type before it is sent; the workflow
 * default is the muted placeholder and an emptied field has no override.
 * The reset button clears an override.
 */
export function ParamOverrideFields({
  defs,
  values,
  editable,
  onChange,
}: {
  /** Parameters of the workflow; positional ones take no override. */
  defs: readonly ParamDef[];
  values: Record<string, string>;
  editable: boolean;
  onChange(name: string, value: OverrideValue | null): void;
}): ReactElement | null {
  const { ts } = useI18n();
  const declared = new Map(
    defs.flatMap((def) => (def.name ? [[def.name, def] as const] : []))
  );
  const overrides = new Map(Object.entries(values));
  const names = [
    ...declared.keys(),
    ...[...overrides.keys()].filter((name) => !declared.has(name)),
  ];
  if (names.length === 0) return null;
  return (
    <div className="space-y-1" role="group" aria-label={ts('Parameters')}>
      {names.map((name) => {
        const def = declared.get(name);
        const overridden = overrides.has(name);
        return (
          <div key={name} className="flex items-start gap-2">
            <div className="min-w-0 flex-1">
              <OverrideControl
                name={name}
                def={def}
                value={overrides.get(name)}
                editable={editable}
                onChange={(value) => onChange(name, value)}
              />
              {!def && (
                <p className="text-xs text-muted-foreground">
                  {ts('Not a parameter of this workflow')}
                </p>
              )}
            </div>
            {editable && (
              // Below the label, beside the control.
              <span className="mt-[18px] flex h-7 w-16 shrink-0 items-center">
                {overridden && (
                  <IconButton
                    size="sm"
                    icon={RotateCcw}
                    label={ts('Use workflow default for {name}', { name })}
                    onClick={() => onChange(name, null)}
                  />
                )}
              </span>
            )}
          </div>
        );
      })}
    </div>
  );
}

function OverrideControl({
  name,
  def,
  value,
  editable,
  onChange,
}: {
  name: string;
  def: ParamDef | undefined;
  /** The override, or undefined when the schedule takes the default. */
  value: string | undefined;
  editable: boolean;
  onChange(value: OverrideValue | null): void;
}): ReactElement {
  const { ts } = useI18n();
  const id = useId();
  const choices = choicesOf(def);
  if (!choices) {
    const placeholder =
      value === ''
        ? EMPTY_OVERRIDE
        : def?.computed
          ? ts('Computed when the run starts')
          : def?.default === undefined
            ? ''
            : scalarText(def.default);
    return (
      <TextField
        label={name}
        value={value ?? ''}
        placeholder={placeholder}
        readOnly={!editable}
        validate={
          def &&
          ((text) =>
            text === '' || DYNAMIC_VALUE.test(text)
              ? undefined
              : validateParamValue(def, text))
        }
        onCommit={(text) =>
          onChange(text === '' ? null : typedValue(def, text))
        }
      />
    );
  }

  // A value outside the list, such as one written in the YAML, stays an
  // option of its own; only a literal one is an error.
  const unknown = value !== undefined && !choices.includes(value);
  const options = unknown ? [...choices, value] : choices;
  const invalid = unknown && !DYNAMIC_VALUE.test(value);
  const defaultLabel = def?.computed
    ? ts('Default (computed when the run starts)')
    : def?.default === undefined
      ? ts('Default (not set)')
      : ts('Default ({value})', { value: scalarText(def.default) });
  return (
    <div className="space-y-0.5">
      <label
        htmlFor={id}
        className="block text-xs font-medium text-muted-foreground"
      >
        {name}
      </label>
      <select
        id={id}
        value={
          value === undefined ? DEFAULT_OPTION : String(options.indexOf(value))
        }
        disabled={!editable}
        aria-invalid={invalid || undefined}
        className={SELECT_CLASS}
        onChange={(event) => {
          const next = event.target.value;
          const option = options[Number(next)];
          onChange(
            next === DEFAULT_OPTION || option === undefined
              ? null
              : typedValue(def, option)
          );
        }}
      >
        <option value={DEFAULT_OPTION}>{defaultLabel}</option>
        {options.map((option, index) => (
          <option key={index} value={String(index)}>
            {option}
          </option>
        ))}
      </select>
      {invalid && (
        <FieldIssues
          issues={[
            {
              severity: ValidationIssueSeverity.error,
              source: ValidationIssueSource.build,
              message: 'Must be one of the allowed values',
            },
          ]}
        />
      )}
    </div>
  );
}
