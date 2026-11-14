// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import type { IChangeEvent } from '@rjsf/core';
import type RJSFForm from '@rjsf/core';
import Form from '@rjsf/shadcn';
import type { FormContextType, RJSFSchema, UiSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import React from 'react';

import { components, ParamDefType } from '../../../../api/v1/schema';
import {
  Parameter,
  parseParams,
  stringifyParams,
} from '../../../../lib/parseParams';
import type { JSONSchema } from '../../../../lib/schema-utils';
import {
  buildParamSchemaFormData,
  buildParamSchemaUiSchema,
  type ParamSchemaUiSchema,
  stringifyParamSchemaFormData,
} from './paramSchemaForm';
import { ParamLabel } from './ParamLabel';
import { paramFormTemplates } from './paramFormTemplates';
import { paramFormWidgets } from './paramFormWidgets';
import { autoGrowTextarea } from './textareaAutoGrow';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type ScalarValue = components['schemas']['ParamScalar'];
type ParamDef = components['schemas']['ParamDef'];
type DAGLike =
  | components['schemas']['DAG']
  | components['schemas']['DAGDetails'];
type SchemaFormData = Record<string, unknown>;

type ParamField = {
  key: string;
  name?: string;
  title?: string;
  type: ParamDefType;
  value: string;
  hasValue: boolean;
  hasDefault: boolean;
  required: boolean;
  /** An eval computes the value when the run starts and none is given. */
  computed: boolean;
  description?: string;
  enum?: ScalarValue[];
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
};

// withSlotHints shows each hint in its param's field: as the placeholder,
// and as help text, which also shows beside a widget without a placeholder,
// such as a date.
function withSlotHints(
  uiSchema: ParamSchemaUiSchema,
  hints: Record<string, string>,
  help: (value: string) => string
): ParamSchemaUiSchema {
  const result = { ...uiSchema };
  for (const [name, hint] of Object.entries(hints)) {
    result[name] = {
      ...result[name],
      'ui:placeholder': hint,
      'ui:help': help(hint),
    };
  }
  return result;
}

// createParamFields starts each field at its default. A computed param starts
// unset, as its default is only the fallback for an eval that fails.
function createParamFields(paramDefs: ParamDef[] = []): ParamField[] {
  return paramDefs.map((def, index) => {
    const computed = def.computed === true;
    const hasDefault = !computed && def.default !== undefined;
    return {
      key: def.name ?? `__pos_${index}`,
      name: def.name,
      title: def.title,
      type: def.type,
      value: hasDefault ? scalarToString(def.default as ScalarValue) : '',
      hasValue: hasDefault,
      hasDefault,
      required: !computed && def.required,
      computed,
      description: def.description,
      enum: def.enum,
      minimum: def.minimum,
      maximum: def.maximum,
      minLength: def.minLength,
      maxLength: def.maxLength,
      pattern: def.pattern,
    };
  });
}

function serializeParamFields(fields: ParamField[]): string {
  const items = fields
    .filter((field) => field.hasValue)
    .map((field) => {
      if (field.name) {
        return { [field.name]: field.value };
      }
      return field.value;
    });

  if (items.length === 0) {
    return '';
  }
  return JSON.stringify(items);
}

function scalarToString(value: ScalarValue): string {
  if (typeof value === 'string') {
    return value;
  }
  return String(value);
}

function validateParamFields(fields: ParamField[]): Record<string, string> {
  const errors: Record<string, string> = {};

  for (const field of fields) {
    if (!field.hasValue) {
      if (field.required) {
        errors[field.key] = 'Required';
      }
      continue;
    }

    const value = field.value;
    switch (field.type) {
      case ParamDefType.string:
        if (field.minLength !== undefined && value.length < field.minLength) {
          errors[field.key] = `Must be at least ${field.minLength} characters`;
          continue;
        }
        if (field.maxLength !== undefined && value.length > field.maxLength) {
          errors[field.key] = `Must be at most ${field.maxLength} characters`;
          continue;
        }
        if (field.pattern) {
          try {
            const pattern = new RegExp(field.pattern);
            if (!pattern.test(value)) {
              errors[field.key] = 'Does not match the required pattern';
              continue;
            }
          } catch {
            errors[field.key] = 'Invalid validation pattern';
            continue;
          }
        }
        if (
          field.enum &&
          !field.enum.some((item) => typeof item === 'string' && item === value)
        ) {
          errors[field.key] = 'Must be one of the allowed values';
        }
        break;

      case ParamDefType.integer: {
        if (!/^-?\d+$/.test(value.trim())) {
          errors[field.key] = 'Must be an integer';
          continue;
        }
        const number = Number(value);
        if (field.minimum !== undefined && number < field.minimum) {
          errors[field.key] = `Must be at least ${field.minimum}`;
          continue;
        }
        if (field.maximum !== undefined && number > field.maximum) {
          errors[field.key] = `Must be at most ${field.maximum}`;
          continue;
        }
        if (
          field.enum &&
          !field.enum.some(
            (item) => typeof item === 'number' && Number(item) === number
          )
        ) {
          errors[field.key] = 'Must be one of the allowed values';
        }
        break;
      }

      case ParamDefType.number: {
        const trimmed = value.trim();
        if (trimmed === '') {
          if (field.required) {
            errors[field.key] = 'Required';
          }
          continue;
        }
        const number = Number(trimmed);
        if (Number.isNaN(number)) {
          errors[field.key] = 'Must be a number';
          continue;
        }
        if (field.minimum !== undefined && number < field.minimum) {
          errors[field.key] = `Must be at least ${field.minimum}`;
          continue;
        }
        if (field.maximum !== undefined && number > field.maximum) {
          errors[field.key] = `Must be at most ${field.maximum}`;
          continue;
        }
        if (
          field.enum &&
          !field.enum.some(
            (item) => typeof item === 'number' && Number(item) === number
          )
        ) {
          errors[field.key] = 'Must be one of the allowed values';
        }
        break;
      }

      case ParamDefType.boolean:
        if (value !== 'true' && value !== 'false') {
          errors[field.key] = 'Must be true or false';
        }
        break;
    }
  }

  return errors;
}

/** The placeholder of a computed param left unset. */
const COMPUTED_HINT = 'Computed when the run starts';

const NO_HINTS: Record<string, string> = {};

/**
 * The params form of a DAG: its values, how a run takes them, and whether
 * they are valid. DAGParamsFields shows it.
 */
export type DAGParamsForm = {
  /** How many params the DAG declares. */
  count: number;
  /** Whether the DAG forbids editing its params. */
  locked: boolean;
  /**
   * The params as the run APIs take them. With changedOnly, it is empty
   * while every value is still the one the form started with, so the
   * caller sends none and the run takes its defaults and evals.
   */
  serialize: (changedOnly?: boolean) => string;
  /** Checks the values, marks the fields in error, and reports success. */
  validate: () => boolean;
  view: ParamsView;
};

type ParamsView = {
  kind: 'schema' | 'typed' | 'raw';
  schema?: JSONSchema;
  paramDefs: ParamDef[];
  computed: ReadonlySet<string>;
  schemaFormData: SchemaFormData;
  setSchemaFormData: (data: SchemaFormData) => void;
  schemaFormRef: React.RefObject<RJSFForm<
    SchemaFormData,
    RJSFSchema,
    FormContextType
  > | null>;
  typedFields: ParamField[];
  changeTypedField: (key: string, patch: Partial<ParamField>) => void;
  fieldErrors: Record<string, string>;
  rawParams: Parameter[];
  changeRawParam: (index: number, value: string) => void;
};

// paramSchemaOf returns the DAG's param schema when it has properties.
function paramSchemaOf(dag: DAGLike | undefined): JSONSchema | undefined {
  const schema = (dag as components['schemas']['DAGDetails'] | undefined)
    ?.paramSchema as JSONSchema | undefined;
  if (!schema || Array.isArray(schema) || typeof schema !== 'object') {
    return undefined;
  }
  if (
    !schema.properties ||
    Array.isArray(schema.properties) ||
    typeof schema.properties !== 'object' ||
    Object.keys(schema.properties).length === 0
  ) {
    return undefined;
  }
  return schema;
}

// withoutComputedDefaults drops the default and the requirement of each
// computed param, so the form leaves it unset and its eval runs.
function withoutComputedDefaults(
  schema: JSONSchema,
  computed: ReadonlySet<string>
): JSONSchema {
  if (computed.size === 0) {
    return schema;
  }
  const properties = { ...schema.properties };
  for (const name of computed) {
    const property = properties[name];
    if (property) {
      const rest = { ...property };
      delete rest.default;
      properties[name] = rest;
    }
  }
  return {
    ...schema,
    properties,
    ...(schema.required
      ? { required: schema.required.filter((name) => !computed.has(name)) }
      : {}),
  };
}

// canonicalFormData is the schema form data in a form two equal values share,
// whatever their keys' order and the empty values the form keeps.
function canonicalFormData(data: SchemaFormData): string {
  return JSON.stringify(
    Object.keys(data)
      .sort()
      .flatMap((key) =>
        data[key] === '' || data[key] === null || data[key] === undefined
          ? []
          : [[key, data[key]]]
      )
  );
}

/** Holds the params form of dag, which starts again each time open is set. */
export function useDAGParamsForm(
  dag: DAGLike | undefined,
  open: boolean
): DAGParamsForm {
  const paramDefs = React.useMemo(
    () =>
      (dag as components['schemas']['DAGDetails'] | undefined)?.paramDefs ?? [],
    [dag]
  );
  const computed = React.useMemo(
    () =>
      new Set(
        paramDefs.flatMap((def) => (def.computed && def.name ? [def.name] : []))
      ),
    [paramDefs]
  );
  const schema = React.useMemo(() => {
    const declared = paramSchemaOf(dag);
    return declared ? withoutComputedDefaults(declared, computed) : undefined;
  }, [dag, computed]);
  const kind: ParamsView['kind'] = schema
    ? 'schema'
    : paramDefs.length > 0
      ? 'typed'
      : 'raw';
  const initialTypedFields = React.useMemo(
    () => createParamFields(paramDefs),
    [paramDefs]
  );
  const initialSchemaFormData = React.useMemo(() => {
    if (!schema) {
      return {};
    }
    const data = buildParamSchemaFormData(schema, dag?.defaultParams);
    for (const name of computed) {
      delete data[name];
    }
    return data;
  }, [computed, dag?.defaultParams, schema]);
  const initialRawParams = React.useMemo(
    () => (dag?.defaultParams ? parseParams(dag.defaultParams) : []),
    [dag?.defaultParams]
  );

  const [schemaFormData, setSchemaFormData] = React.useState<SchemaFormData>(
    {}
  );
  const [typedFields, setTypedFields] = React.useState<ParamField[]>([]);
  const [rawParams, setRawParams] = React.useState<Parameter[]>([]);
  const [fieldErrors, setFieldErrors] = React.useState<Record<string, string>>(
    {}
  );
  const schemaFormRef = React.useRef<RJSFForm<
    SchemaFormData,
    RJSFSchema,
    FormContextType
  > | null>(null);

  React.useEffect(() => {
    if (!open) {
      return;
    }
    setSchemaFormData(initialSchemaFormData);
    setTypedFields(initialTypedFields);
    setRawParams(initialRawParams);
    setFieldErrors({});
  }, [open, initialSchemaFormData, initialTypedFields, initialRawParams]);

  const changeTypedField = React.useCallback(
    (key: string, patch: Partial<ParamField>) => {
      setTypedFields((prev) =>
        prev.map((field) =>
          field.key === key ? { ...field, ...patch } : field
        )
      );
      setFieldErrors((prev) => {
        if (!prev[key]) {
          return prev;
        }
        const next = { ...prev };
        delete next[key];
        return next;
      });
    },
    []
  );
  const changeRawParam = React.useCallback((index: number, value: string) => {
    setRawParams((prev) =>
      prev.map((item, itemIndex) =>
        itemIndex === index ? { ...item, Value: value } : item
      )
    );
  }, []);

  const serialize = React.useCallback(
    (changedOnly = false) => {
      switch (kind) {
        case 'schema':
          return changedOnly &&
            canonicalFormData(schemaFormData) ===
              canonicalFormData(initialSchemaFormData)
            ? ''
            : stringifyParamSchemaFormData(schemaFormData);
        case 'typed': {
          const value = serializeParamFields(typedFields);
          return changedOnly &&
            value === serializeParamFields(initialTypedFields)
            ? ''
            : value;
        }
        case 'raw': {
          const value = stringifyParams(rawParams);
          return changedOnly && value === stringifyParams(initialRawParams)
            ? ''
            : value;
        }
      }
    },
    [
      initialRawParams,
      initialSchemaFormData,
      initialTypedFields,
      kind,
      rawParams,
      schemaFormData,
      typedFields,
    ]
  );

  const validate = React.useCallback(() => {
    if (kind === 'schema') {
      return schemaFormRef.current?.validateForm() ?? true;
    }
    if (kind === 'typed') {
      const errors = validateParamFields(typedFields);
      setFieldErrors(errors);
      return Object.keys(errors).length === 0;
    }
    return true;
  }, [kind, typedFields]);

  const count =
    kind === 'schema'
      ? Object.keys(schema?.properties ?? {}).length
      : kind === 'typed'
        ? paramDefs.length
        : initialRawParams.length;
  const runConfig = (
    dag as
      | (DAGLike & { runConfig?: { disableParamEdit?: boolean } })
      | undefined
  )?.runConfig;
  // A workflow without parameters has none to lock.
  const locked = (runConfig?.disableParamEdit ?? false) && count > 0;

  return {
    count,
    locked,
    serialize,
    validate,
    view: {
      kind,
      schema,
      paramDefs,
      computed,
      schemaFormData,
      setSchemaFormData,
      schemaFormRef,
      typedFields,
      changeTypedField,
      fieldErrors,
      rawParams,
      changeRawParam,
    },
  };
}

type FieldsProps = {
  form: DAGParamsForm;
  /**
   * The value a param takes when left empty, by param name, as a run's
   * slot gives it. It shows as the field's placeholder.
   */
  slotHints?: Record<string, string>;
  disabled?: boolean;
  /** Called on each edit. */
  onEdit?: () => void;
};

/** The fields of a DAG's params form. */
export function DAGParamsFields({
  form,
  slotHints = NO_HINTS,
  disabled = false,
  onEdit,
}: FieldsProps) {
  const { ts } = useI18n();
  const { view, locked } = form;
  const uiSchema = React.useMemo<UiSchema<SchemaFormData> | undefined>(() => {
    if (!view.schema) {
      return undefined;
    }
    const base = buildParamSchemaUiSchema(view.schema);
    for (const name of view.computed) {
      base[name] = { ...base[name], 'ui:placeholder': ts(COMPUTED_HINT) };
    }
    return {
      ...withSlotHints(base, slotHints, (value) =>
        ts('Left empty, it takes {value} from the slot.', { value })
      ),
      'ui:order': view.paramDefs
        .flatMap(({ name }) => (name ? [name] : []))
        .concat('*'),
      'ui:submitButtonOptions': { norender: true },
    } as UiSchema<SchemaFormData>;
  }, [slotHints, ts, view.computed, view.paramDefs, view.schema]);

  if (view.kind === 'schema' && view.schema) {
    return (
      <Form
        ref={view.schemaFormRef}
        tagName="div"
        schema={view.schema as RJSFSchema}
        validator={validator}
        formData={view.schemaFormData}
        uiSchema={uiSchema}
        templates={paramFormTemplates}
        widgets={paramFormWidgets}
        disabled={locked || disabled}
        readonly={locked}
        noHtml5Validate
        showErrorList={false}
        onChange={(event: IChangeEvent<SchemaFormData>) => {
          view.setSchemaFormData((event.formData ?? {}) as SchemaFormData);
          onEdit?.();
        }}
      />
    );
  }

  if (view.kind === 'typed') {
    return (
      <>
        {view.typedFields.map((field, index) => {
          const label = field.title || field.name || `Parameter ${index + 1}`;
          const error = view.fieldErrors[field.key];
          const descriptionId = `param-${field.key}-description`;
          const errorId = `param-${field.key}-error`;
          const describedBy =
            [field.description && descriptionId, error && errorId]
              .filter(Boolean)
              .join(' ') || undefined;
          const disableField = locked || disabled;
          const showUnsetButton =
            !field.required && !field.hasDefault && field.hasValue;
          const change = (patch: Partial<ParamField>) => {
            view.changeTypedField(field.key, patch);
            onEdit?.();
          };

          return (
            <div key={field.key} className="space-y-2">
              <div className="flex items-center justify-between gap-3">
                <ParamLabel
                  htmlFor={`param-${field.key}`}
                  name={field.name}
                  label={label}
                >
                  {label}
                  {field.required ? ' *' : ''}
                </ParamLabel>
                {showUnsetButton && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2 text-xs"
                    disabled={disableField}
                    onClick={() => change({ hasValue: false, value: '' })}
                  >
                    <I18nText text={'Unset'} />
                  </Button>
                )}
              </div>

              {field.description && (
                <p id={descriptionId} className="text-xs text-muted-foreground">
                  {field.description}
                </p>
              )}

              {renderTypedField({
                field,
                hint:
                  (field.name ? slotHints[field.name] : undefined) ??
                  (field.computed ? ts(COMPUTED_HINT) : undefined),
                disabled: disableField,
                describedBy,
                invalid: !!error,
                onChange: change,
              })}

              {error && (
                <p id={errorId} className="text-xs text-destructive">
                  {error}
                </p>
              )}
            </div>
          );
        })}
      </>
    );
  }

  return (
    <>
      {view.rawParams.map((param, index) => (
        <div key={`${param.Name ?? 'pos'}-${index}`} className="space-y-2">
          <Label htmlFor={`param-${index}`}>
            {param.Name ?? `Parameter ${index + 1}`}
          </Label>
          <Textarea
            id={`param-${index}`}
            ref={(element) => {
              if (element) {
                autoGrowTextarea(element);
              }
            }}
            rows={1}
            value={param.Value ?? ''}
            readOnly={locked}
            disabled={locked || disabled}
            className={locked ? 'bg-muted cursor-not-allowed' : ''}
            onInput={(event) => autoGrowTextarea(event.currentTarget)}
            onChange={(event) => {
              if (locked) {
                return;
              }
              view.changeRawParam(index, event.target.value);
              onEdit?.();
            }}
          />
        </div>
      ))}
    </>
  );
}

function renderTypedField({
  field,
  hint,
  disabled,
  describedBy,
  invalid,
  onChange,
}: {
  field: ParamField;
  /** The value the param takes when left unset, shown as its placeholder. */
  hint?: string;
  disabled: boolean;
  describedBy?: string;
  invalid: boolean;
  onChange: (patch: Partial<ParamField>) => void;
}) {
  const controlClass = 'w-full';

  if (field.enum && field.enum.length > 0) {
    const placeholder = field.hasValue ? field.value : 'Select a value';
    return (
      <Select
        value={field.hasValue ? field.value : undefined}
        disabled={disabled}
        onValueChange={(value) => {
          if (value === '__unset__') {
            onChange({ value: '', hasValue: false });
            return;
          }
          onChange({ value, hasValue: true });
        }}
      >
        <SelectTrigger
          id={`param-${field.key}`}
          aria-describedby={describedBy}
          aria-invalid={invalid || undefined}
          className={controlClass}
        >
          <SelectValue placeholder={placeholder} />
        </SelectTrigger>
        <SelectContent>
          {!field.required && !field.hasDefault && (
            <SelectItem value="__unset__">
              <I18nText text={'Not set'} />
            </SelectItem>
          )}
          {field.enum.map((item, index) => {
            const value = scalarToString(item);
            return (
              <SelectItem key={`${field.key}-${index}-${value}`} value={value}>
                {value}
              </SelectItem>
            );
          })}
        </SelectContent>
      </Select>
    );
  }

  switch (field.type) {
    case ParamDefType.integer:
    case ParamDefType.number:
      return (
        <I18nProps>
          <Input
            id={`param-${field.key}`}
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            type="number"
            className={controlClass}
            min={field.minimum}
            max={field.maximum}
            step={field.type === ParamDefType.integer ? 1 : 'any'}
            value={field.hasValue ? field.value : ''}
            placeholder={field.hasValue ? undefined : (hint ?? 'Not set')}
            disabled={disabled}
            onChange={(event) =>
              onChange({
                value: event.target.value,
                hasValue: event.target.value !== '',
              })
            }
          />
        </I18nProps>
      );

    case ParamDefType.boolean:
      if (!field.required && !field.hasDefault && !field.hasValue) {
        return (
          <Select
            value={field.hasValue ? field.value : '__unset__'}
            disabled={disabled}
            onValueChange={(value) => {
              if (value === '__unset__') {
                onChange({ value: '', hasValue: false });
                return;
              }
              onChange({ value, hasValue: true });
            }}
          >
            <SelectTrigger
              id={`param-${field.key}`}
              aria-describedby={describedBy}
              aria-invalid={invalid || undefined}
              className={controlClass}
            >
              <I18nProps>
                <SelectValue placeholder="Not set" />
              </I18nProps>
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__unset__">
                <I18nText text={'Not set'} />
              </SelectItem>
              <SelectItem value="true">
                <I18nText text={'true'} />
              </SelectItem>
              <SelectItem value="false">
                <I18nText text={'false'} />
              </SelectItem>
            </SelectContent>
          </Select>
        );
      }
      return (
        <div className="flex items-center gap-3 rounded-md border border-border px-3 py-2">
          <Switch
            id={`param-${field.key}`}
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            checked={field.value === 'true'}
            disabled={disabled}
            onCheckedChange={(checked) =>
              onChange({ value: checked ? 'true' : 'false', hasValue: true })
            }
          />
          <span className="text-sm text-muted-foreground">
            {field.value === 'true' ? (
              <I18nText text={'true'} />
            ) : (
              <I18nText text={'false'} />
            )}
          </span>
        </div>
      );

    case ParamDefType.string:
    default:
      return (
        <I18nProps>
          <Textarea
            id={`param-${field.key}`}
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            className={`${controlClass} h-7 min-h-7 py-1`}
            rows={1}
            ref={(element) => {
              if (element) {
                autoGrowTextarea(element);
              }
            }}
            value={field.hasValue ? field.value : ''}
            placeholder={field.hasValue ? undefined : (hint ?? 'Not set')}
            disabled={disabled}
            onInput={(event) => autoGrowTextarea(event.currentTarget)}
            onChange={(event) =>
              onChange({
                value: event.target.value,
                hasValue: true,
              })
            }
          />
        </I18nProps>
      );
  }
}
