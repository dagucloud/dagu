// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Form, { Templates } from '@rjsf/shadcn';
import type { IChangeEvent } from '@rjsf/core';
import type { FieldTemplateProps, RJSFSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type ReactElement,
} from 'react';
import { schemaFormTemplates } from '@/features/dags/components/dag-execution/schemaFormTemplates';
import { I18nText } from '@/i18n/I18nText';
import { cn } from '@/lib/utils';
import type {
  EditorCustomActionHint,
  JSONSchema,
  ReferenceCandidate,
} from '../../contracts';
import { FieldIssues } from '../FieldIssues';
import { diffFormData, sameValue, toFieldOps } from '../formDiff';
import { issuesUnder } from '../issues';
import {
  formSchemaOf,
  isPlainObject,
  issuesToErrorSchema,
  splitWithValue,
} from '../paramsForm';
import type { SectionProps } from '../types';
import {
  buildUiSchema,
  inspectorFields,
  inspectorTemplates,
  inspectorWidgets,
  RouteTable,
  type InspectorFormContext,
} from '../widgets';
import {
  getActionWithSchema,
  isLargeActionSchema,
  reduceToEssentialFields,
} from '../withSchema';

export interface ParametersSectionProps extends SectionProps {
  schema: JSONSchema;
  customActions: EditorCustomActionHint[];
  candidates: ReferenceCandidate[];
}

/** Time between the last form change and the field edits that it sends. */
export const PARAMETERS_DEBOUNCE_MS = 400;

/** rjsf must not write defaults: opening a form must not change the step. */
const NO_DEFAULTS = {
  emptyObjectFields: 'skipDefaults',
  arrayMinItems: { populate: 'never' },
  allOf: 'skipDefaults',
  constAsDefaults: 'never',
} as const;

/**
 * One height for the text, number and select controls of the form, and a gap
 * under the description of a group. The widgets of the theme set their own.
 */
export const FORM_CONTROLS =
  '[&_input:not([type=checkbox]):not([type=radio])]:h-7 [&_[data-slot=select-trigger]]:h-7 [&_textarea]:min-h-7 [&_[id$=__description]]:mb-2';

/** Field path of a form value in the step: `with.url`, `with.items[0]`. */
export function withFieldPath(path: (string | number)[]): string {
  return path.reduce<string>(
    (text, part) =>
      typeof part === 'number' ? `${text}[${part}]` : `${text}.${part}`,
    'with'
  );
}

const BaseFieldTemplate =
  Templates.FieldTemplate as ComponentType<FieldTemplateProps>;

/**
 * The field template of the theme in a box that names the field path, so
 * that a host can find the field. It also tells whether the field is
 * required and empty.
 */
function PathFieldTemplate(props: FieldTemplateProps): ReactElement {
  const path = props.fieldPathId.path;
  if (path.length === 0) {
    return <BaseFieldTemplate {...props} />;
  }
  const empty =
    props.formData === undefined ||
    props.formData === null ||
    props.formData === '';
  return (
    <div
      data-field-path={withFieldPath(path)}
      data-required={props.required ? 'true' : undefined}
      data-empty={empty ? 'true' : undefined}
    >
      <BaseFieldTemplate {...props} />
    </div>
  );
}

const formTemplates = {
  ...schemaFormTemplates,
  ...inspectorTemplates,
  FieldTemplate: PathFieldTemplate,
};

function OtherKeys({ entries }: { entries: [string, unknown][] }) {
  if (entries.length === 0) {
    return null;
  }
  return (
    <div className="space-y-0.5" data-testid="ve-other-keys">
      <div className="text-xs font-medium text-muted-foreground">
        <I18nText text="Other keys" />
      </div>
      <ul className="space-y-0.5 text-xs">
        {entries.map(([key, value]) => (
          <li key={key} className="flex gap-1">
            <span className="font-mono text-foreground">{key}</span>
            <span className="truncate text-muted-foreground">
              {JSON.stringify(value)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * Form of the `with` block of an action step, built from the `with`
 * schema of the action. Changes are sent after a short pause, on blur and
 * on unmount, as one `setStepField` for each changed leaf. Keys that the
 * schema does not know are shown read-only and kept. Large schemas show
 * their essential fields only; `router.route` shows `value` and a route
 * table.
 */
export function ParametersSection({
  step,
  readOnly,
  onEdit,
  fieldIssues,
  schema,
  customActions,
  candidates,
}: ParametersSectionProps): ReactElement {
  const action =
    typeof step.value.action === 'string'
      ? step.value.action
      : (step.action ?? '');
  const isRouter = action === 'router.route';
  const large = isLargeActionSchema(action);

  const fullSchema = useMemo(
    () => (action ? getActionWithSchema(schema, action, customActions) : null),
    [schema, action, customActions]
  );
  const formSchema = useMemo(() => {
    if (!fullSchema?.properties) {
      return null;
    }
    const base = large ? reduceToEssentialFields(fullSchema) : fullSchema;
    return formSchemaOf(base, isRouter ? ['value'] : undefined);
  }, [fullSchema, large, isRouter]);

  const known = useMemo(
    () => Object.keys(fullSchema?.properties ?? {}),
    [fullSchema]
  );
  const shown = useMemo(
    () => Object.keys(formSchema?.properties ?? {}),
    [formSchema]
  );
  const { formData: base, other } = useMemo(
    () => splitWithValue(step.value.with, known, shown),
    [step.value.with, known, shown]
  );

  const [draft, setDraft] = useState(base);
  const sent = useRef(base);
  const pending = useRef<Record<string, unknown> | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!sameValue(base, sent.current)) {
      sent.current = base;
      pending.current = null;
      setDraft(base);
    }
  }, [base]);

  const flush = useCallback(() => {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    const next = pending.current;
    pending.current = null;
    if (!next || readOnly) {
      return;
    }
    const changes = diffFormData(sent.current, next, ['with']);
    if (changes.length > 0) {
      sent.current = next;
      for (const op of toFieldOps(step.key, changes)) {
        onEdit(op);
      }
    }
  }, [readOnly, step.key, onEdit]);

  const flushRef = useRef(flush);
  flushRef.current = flush;
  useEffect(() => () => flushRef.current(), []);

  const onChange = (event: IChangeEvent) => {
    if (readOnly) {
      return;
    }
    const next = isPlainObject(event.formData) ? event.formData : {};
    setDraft(next);
    pending.current = next;
    if (timer.current) {
      clearTimeout(timer.current);
    }
    timer.current = setTimeout(
      () => flushRef.current(),
      PARAMETERS_DEBOUNCE_MS
    );
  };

  const uiSchema = useMemo(
    () => ({
      ...(formSchema ? buildUiSchema(formSchema) : {}),
      'ui:submitButtonOptions': { norender: true },
    }),
    [formSchema]
  );
  const extraErrors = useMemo(
    () => issuesToErrorSchema(fieldIssues),
    [fieldIssues]
  );
  const formContext: InspectorFormContext = useMemo(
    () => ({ candidates }),
    [candidates]
  );
  const routes = isPlainObject(step.value.with)
    ? step.value.with.routes
    : undefined;

  if (!formSchema) {
    return (
      <div className="space-y-1" data-field-path="with">
        <p className="text-xs text-muted-foreground">
          <I18nText text="No form for the parameters of this action. Edit them in the YAML section." />
        </p>
        <FieldIssues issues={issuesUnder(fieldIssues, 'with')} />
        <OtherKeys
          entries={
            isPlainObject(step.value.with)
              ? Object.entries(step.value.with)
              : []
          }
        />
      </div>
    );
  }

  return (
    <div
      className={cn('space-y-2', FORM_CONTROLS)}
      data-field-path="with"
      onBlur={flush}
    >
      {large && (
        <p className="text-xs text-muted-foreground">
          <I18nText text="Edit the other fields in YAML" />
        </p>
      )}
      <Form
        tagName="div"
        schema={formSchema as RJSFSchema}
        formData={draft}
        validator={validator}
        widgets={inspectorWidgets}
        fields={inspectorFields}
        templates={formTemplates}
        uiSchema={uiSchema}
        formContext={formContext}
        extraErrors={extraErrors}
        experimental_defaultFormStateBehavior={NO_DEFAULTS}
        disabled={readOnly}
        readonly={readOnly}
        noHtml5Validate
        showErrorList={false}
        onChange={onChange}
      />
      {isRouter && (
        <div className="space-y-0.5" data-field-path="with.routes">
          <div className="text-xs font-medium text-muted-foreground">
            <I18nText text="Routes" />
          </div>
          <RouteTable
            routes={
              isPlainObject(routes) ? (routes as Record<string, string[]>) : {}
            }
            readOnly={readOnly}
            onSetTargets={(pattern, targets) =>
              onEdit({
                kind: 'setRouteTargets',
                step: step.key,
                pattern,
                targets,
              })
            }
          />
          <FieldIssues issues={issuesUnder(fieldIssues, 'with.routes')} />
        </div>
      )}
      <OtherKeys entries={other} />
    </div>
  );
}
