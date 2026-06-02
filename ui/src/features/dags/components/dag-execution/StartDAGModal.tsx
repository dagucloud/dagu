// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import { useIsAdmin } from '@/contexts/AuthContext';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import type { IChangeEvent } from '@rjsf/core';
import type RJSFForm from '@rjsf/core';
import Form from '@rjsf/shadcn';
import type { RJSFSchema, UiSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import { AlertTriangle, ListPlus, Play, X } from 'lucide-react';
import React from 'react';

import {
  components,
  ParamDefType,
  RuntimeProfileStatus,
} from '../../../../api/v1/schema';
import {
  Parameter,
  parseParams,
  stringifyParams,
} from '../../../../lib/parseParams';
import type { JSONSchema } from '../../../../lib/schema-utils';
import {
  buildParamSchemaFormData,
  buildParamSchemaUiSchema,
  stringifyParamSchemaFormData,
} from './paramSchemaForm';
import { schemaFormTemplates } from './schemaFormTemplates';
import { schemaFormWidgets } from './schemaFormWidgets';
import { autoGrowTextarea } from './textareaAutoGrow';

type ScalarValue = components['schemas']['ParamScalar'];
type ParamDef = components['schemas']['ParamDef'];
type DAGLike =
  | components['schemas']['DAG']
  | components['schemas']['DAGDetails'];
type SchemaFormData = Record<string, unknown>;

type ParamField = {
  key: string;
  name?: string;
  type: ParamDefType;
  value: string;
  hasValue: boolean;
  hasDefault: boolean;
  required: boolean;
  description?: string;
  enum?: ScalarValue[];
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  pattern?: string;
};

type Props = {
  visible: boolean;
  dag?: DAGLike;
  loading?: boolean;
  loadError?: string | null;
  dismissModal: () => void;
  onSubmit: (
    params: string,
    dagRunId?: string,
    immediate?: boolean,
    profile?: string
  ) => Promise<void> | void;
  action?: 'start' | 'enqueue';
  profiles?: components['schemas']['RuntimeProfileResponse'][];
  profilesLoading?: boolean;
  defaultProfile?: string;
  defaultProfileLoading?: boolean;
};

const DAG_DEFAULT_PROFILE_VALUE = '__dag_default__';
const NO_PROFILE_VALUE = '__none__';

function profileOverrideForSelection(
  selection: string,
  hasDefaultProfile: boolean
): string | undefined {
  if (selection === DAG_DEFAULT_PROFILE_VALUE) {
    return undefined;
  }
  if (selection === NO_PROFILE_VALUE) {
    return hasDefaultProfile ? '' : undefined;
  }
  return selection;
}

function createParamFields(paramDefs: ParamDef[] = []): ParamField[] {
  return paramDefs.map((def, index) => {
    const hasDefault = def.default !== undefined;
    return {
      key: def.name ?? `__pos_${index}`,
      name: def.name,
      type: def.type,
      value: hasDefault ? scalarToString(def.default as ScalarValue) : '',
      hasValue: hasDefault,
      hasDefault,
      required: def.required,
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

function StartDAGModal({
  visible,
  dag,
  loading = false,
  loadError,
  dismissModal,
  onSubmit,
  action,
  profiles = [],
  profilesLoading = false,
  defaultProfile = '',
  defaultProfileLoading = false,
}: Props) {
  const canUseProtectedProfiles = useIsAdmin();
  const dagDetails = dag as components['schemas']['DAGDetails'] | undefined;
  const paramSchema = React.useMemo(() => {
    const schema = dagDetails?.paramSchema as JSONSchema | undefined;
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
  }, [dagDetails]);
  const paramDefs = React.useMemo(
    () => dagDetails?.paramDefs ?? [],
    [dagDetails]
  );
  const useSchemaFields = !!paramSchema;
  const useTypedFields = !useSchemaFields && paramDefs.length > 0;
  const initialTypedFields = React.useMemo(
    () => createParamFields(paramDefs),
    [paramDefs]
  );
  const initialSchemaFormData = React.useMemo(
    () =>
      paramSchema
        ? buildParamSchemaFormData(paramSchema, dag?.defaultParams)
        : {},
    [dag?.defaultParams, paramSchema]
  );
  const initialRawParams = React.useMemo(() => {
    if (!dag?.defaultParams) {
      return [] as Parameter[];
    }
    return parseParams(dag.defaultParams);
  }, [dag?.defaultParams]);

  const [schemaFormData, setSchemaFormData] = React.useState<SchemaFormData>(
    {}
  );
  const [typedFields, setTypedFields] = React.useState<ParamField[]>([]);
  const [rawParams, setRawParams] = React.useState<Parameter[]>([]);
  const [fieldErrors, setFieldErrors] = React.useState<Record<string, string>>(
    {}
  );
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const [dagRunId, setDAGRunId] = React.useState('');
  const [profileSelection, setProfileSelection] = React.useState(
    defaultProfile ? DAG_DEFAULT_PROFILE_VALUE : NO_PROFILE_VALUE
  );
  const forceEnqueue = action === 'enqueue';
  const [enqueue, setEnqueue] = React.useState(forceEnqueue);
  const activeProfiles = React.useMemo(
    () =>
      profiles.filter(
        (profile) => profile.status === RuntimeProfileStatus.active
      ),
    [profiles]
  );
  const hasDefaultProfile = defaultProfile !== '';
  const showProfileSelector =
    defaultProfileLoading ||
    profilesLoading ||
    hasDefaultProfile ||
    activeProfiles.length > 0;

  React.useEffect(() => {
    if (
      canUseProtectedProfiles ||
      profileSelection === '' ||
      profileSelection === DAG_DEFAULT_PROFILE_VALUE ||
      profileSelection === NO_PROFILE_VALUE
    ) {
      return;
    }
    const selectedProfile = activeProfiles.find(
      (profile) => profile.name === profileSelection
    );
    if (selectedProfile?.protected) {
      setProfileSelection(
        hasDefaultProfile ? DAG_DEFAULT_PROFILE_VALUE : NO_PROFILE_VALUE
      );
    }
  }, [
    activeProfiles,
    canUseProtectedProfiles,
    hasDefaultProfile,
    profileSelection,
  ]);

  const dagWithRunConfig = dag as DAGLike & {
    runConfig?: { disableParamEdit?: boolean; disableRunIdEdit?: boolean };
  };

  const paramsReadOnly = dagWithRunConfig?.runConfig?.disableParamEdit ?? false;
  const runIdReadOnly = dagWithRunConfig?.runConfig?.disableRunIdEdit ?? false;
  const schemaFormUiSchema = React.useMemo<
    UiSchema<SchemaFormData> | undefined
  >(
    () =>
      paramSchema
        ? ({
            ...buildParamSchemaUiSchema(paramSchema),
            'ui:submitButtonOptions': { norender: true },
          } as UiSchema<SchemaFormData>)
        : undefined,
    [paramSchema]
  );
  const schemaFormRef = React.useRef<RJSFForm<
    SchemaFormData,
    RJSFSchema,
    any
  > | null>(null);

  React.useEffect(() => {
    if (!visible) {
      return;
    }
    setSchemaFormData(initialSchemaFormData);
    setTypedFields(initialTypedFields);
    setRawParams(initialRawParams);
    setFieldErrors({});
    setSubmitError(null);
    setSubmitting(false);
    setDAGRunId('');
    setProfileSelection(
      defaultProfile ? DAG_DEFAULT_PROFILE_VALUE : NO_PROFILE_VALUE
    );
    setEnqueue(forceEnqueue);
  }, [
    defaultProfile,
    visible,
    initialSchemaFormData,
    initialTypedFields,
    initialRawParams,
    forceEnqueue,
  ]);

  const cancelButtonRef = React.useRef<HTMLButtonElement>(null);

  const handleTypedFieldChange = React.useCallback(
    (fieldKey: string, patch: Partial<ParamField>) => {
      setTypedFields((prev) =>
        prev.map((field) =>
          field.key === fieldKey
            ? {
                ...field,
                ...patch,
              }
            : field
        )
      );
      setFieldErrors((prev) => {
        if (!prev[fieldKey]) {
          return prev;
        }
        const next = { ...prev };
        delete next[fieldKey];
        return next;
      });
      setSubmitError(null);
    },
    []
  );

  const handleSubmit = React.useCallback(async () => {
    if (!dag || loading || !!loadError || submitting) {
      return;
    }

    let paramsPayload = '';
    if (useSchemaFields) {
      const isValid = schemaFormRef.current?.validateForm() ?? true;
      if (!isValid) {
        setSubmitError(
          'Fix the highlighted parameter errors before submitting.'
        );
        return;
      }
      paramsPayload = stringifyParamSchemaFormData(schemaFormData);
    } else if (useTypedFields) {
      const errors = validateParamFields(typedFields);
      setFieldErrors(errors);
      if (Object.keys(errors).length > 0) {
        setSubmitError(
          'Fix the highlighted parameter errors before submitting.'
        );
        return;
      }
      paramsPayload = serializeParamFields(typedFields);
    } else {
      paramsPayload = stringifyParams(rawParams);
    }

    setSubmitting(true);
    setSubmitError(null);
    try {
      const profileOverride = profileOverrideForSelection(
        profileSelection,
        hasDefaultProfile
      );
      if (profileOverride === undefined) {
        await onSubmit(paramsPayload, dagRunId || undefined, !enqueue);
      } else {
        await onSubmit(
          paramsPayload,
          dagRunId || undefined,
          !enqueue,
          profileOverride
        );
      }
      dismissModal();
    } catch (error) {
      setSubmitError(
        error instanceof Error ? error.message : 'Failed to start DAG'
      );
    } finally {
      setSubmitting(false);
    }
  }, [
    dag,
    dagRunId,
    dismissModal,
    enqueue,
    hasDefaultProfile,
    loadError,
    loading,
    onSubmit,
    rawParams,
    schemaFormData,
    useSchemaFields,
    submitting,
    profileSelection,
    typedFields,
    useTypedFields,
  ]);

  React.useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!visible || event.isComposing) {
        return;
      }

      if (event.key !== 'Enter') {
        return;
      }

      const activeElement = document.activeElement;
      if (activeElement === cancelButtonRef.current) {
        event.preventDefault();
        dismissModal();
        return;
      }
      if (activeElement instanceof HTMLButtonElement) {
        return;
      }
      if (activeElement instanceof HTMLTextAreaElement) {
        return;
      }
      if (
        activeElement instanceof HTMLInputElement ||
        activeElement instanceof HTMLSelectElement ||
        !activeElement
      ) {
        event.preventDefault();
        void handleSubmit();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [dismissModal, handleSubmit, visible]);

  return (
    <Dialog open={visible} onOpenChange={(open) => !open && dismissModal()}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>
            {forceEnqueue ? 'Enqueue the DAG' : 'Start the DAG'}
          </DialogTitle>
          <DialogDescription className="sr-only">
            Configure the DAG run before submitting it.
          </DialogDescription>
        </DialogHeader>

        {(paramsReadOnly || runIdReadOnly) && (
          <div className="bg-warning-muted border border-warning/30 rounded-md p-3">
            <p className="text-sm text-warning">
              <strong>Note:</strong> This DAG has restrictions:
              {paramsReadOnly && runIdReadOnly && (
                <span> Parameter editing and custom run IDs are disabled.</span>
              )}
              {paramsReadOnly && !runIdReadOnly && (
                <span> Parameter editing is disabled.</span>
              )}
              {!paramsReadOnly && runIdReadOnly && (
                <span> Custom run IDs are disabled.</span>
              )}
            </p>
          </div>
        )}

        {(loadError || submitError) && (
          <Alert variant="destructive">
            <AlertTriangle className="h-4 w-4" />
            <AlertDescription>{loadError ?? submitError}</AlertDescription>
          </Alert>
        )}

        <div className="-mx-1 max-h-[60vh] space-y-4 overflow-y-auto px-1 py-4">
          {!forceEnqueue && (
            <div className="flex items-center space-x-2">
              <Checkbox
                id="enqueue"
                checked={enqueue}
                onCheckedChange={(checked) => setEnqueue(checked as boolean)}
                disabled={loading || submitting}
              />
              <Label htmlFor="enqueue" className="cursor-pointer">
                Enqueue
              </Label>
            </div>
          )}

          <div className="space-y-2">
            <Label htmlFor="dagRun-id">DAG-Run ID (optional)</Label>
            <Input
              id="dagRun-id"
              placeholder="Enter custom DAG-Run ID"
              value={dagRunId}
              readOnly={runIdReadOnly}
              disabled={runIdReadOnly || loading || submitting}
              className={runIdReadOnly ? 'bg-muted cursor-not-allowed' : ''}
              onChange={(event) => {
                if (!runIdReadOnly) {
                  setDAGRunId(event.target.value);
                }
              }}
            />
          </div>

          {showProfileSelector && (
            <div className="space-y-2">
              <Label htmlFor="runtime-profile">Profile</Label>
              <Select
                value={profileSelection}
                disabled={
                  loading ||
                  submitting ||
                  profilesLoading ||
                  defaultProfileLoading
                }
                onValueChange={setProfileSelection}
              >
                <SelectTrigger id="runtime-profile" className="w-full">
                  <SelectValue placeholder="No profile" />
                </SelectTrigger>
                <SelectContent>
                  {hasDefaultProfile && (
                    <SelectItem value={DAG_DEFAULT_PROFILE_VALUE}>
                      <span className="flex w-full items-center justify-between gap-3">
                        <span>DAG default</span>
                        <span className="truncate text-xs text-muted-foreground">
                          {defaultProfile}
                        </span>
                      </span>
                    </SelectItem>
                  )}
                  <SelectItem value={NO_PROFILE_VALUE}>No profile</SelectItem>
                  {activeProfiles.map((profile) => {
                    const protectedUnavailable =
                      profile.protected && !canUseProtectedProfiles;
                    return (
                      <SelectItem
                        key={profile.id}
                        value={profile.name}
                        disabled={protectedUnavailable}
                      >
                        <span className="flex w-full items-center justify-between gap-3">
                          <span>{profile.name}</span>
                          <span className="flex items-center gap-1.5">
                            {profile.protected && (
                              <Badge
                                variant="outline"
                                className="h-4 px-1.5 text-[10px]"
                              >
                                Protected
                              </Badge>
                            )}
                            {protectedUnavailable && (
                              <Badge
                                variant="secondary"
                                className="h-4 px-1.5 text-[10px]"
                              >
                                Admin
                              </Badge>
                            )}
                          </span>
                        </span>
                      </SelectItem>
                    );
                  })}
                </SelectContent>
              </Select>
            </div>
          )}

          {loading && (
            <div className="rounded-md border border-border bg-muted/40 p-3 text-sm text-muted-foreground">
              Loading DAG details...
            </div>
          )}

          {!loading && dag && paramSchema && (
            <Form
              ref={schemaFormRef}
              tagName="div"
              schema={paramSchema as RJSFSchema}
              validator={validator}
              formData={schemaFormData}
              uiSchema={schemaFormUiSchema}
              templates={schemaFormTemplates}
              widgets={schemaFormWidgets}
              disabled={paramsReadOnly || submitting}
              readonly={paramsReadOnly}
              noHtml5Validate
              showErrorList={false}
              onChange={(event: IChangeEvent<SchemaFormData>) => {
                setSchemaFormData((event.formData ?? {}) as SchemaFormData);
                setSubmitError(null);
              }}
              onError={() =>
                setSubmitError(
                  'Fix the highlighted parameter errors before submitting.'
                )
              }
            />
          )}

          {!loading && dag && useTypedFields && (
            <>
              {typedFields.map((field, index) => {
                const label = field.name || `Parameter ${index + 1}`;
                const error = fieldErrors[field.key];
                const disableField = paramsReadOnly || submitting;
                const showUnsetButton =
                  !field.required && !field.hasDefault && field.hasValue;

                return (
                  <div key={field.key} className="space-y-2">
                    <div className="flex items-center justify-between gap-3">
                      <Label htmlFor={`param-${field.key}`}>
                        {label}
                        {field.required ? ' *' : ''}
                      </Label>
                      {showUnsetButton && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          disabled={disableField}
                          onClick={() =>
                            handleTypedFieldChange(field.key, {
                              hasValue: false,
                              value: '',
                            })
                          }
                        >
                          Unset
                        </Button>
                      )}
                    </div>

                    {renderTypedField({
                      field,
                      disabled: disableField,
                      onChange: (patch) =>
                        handleTypedFieldChange(field.key, patch),
                    })}

                    {field.description && (
                      <p className="text-xs text-muted-foreground">
                        {field.description}
                      </p>
                    )}
                    {error && (
                      <p className="text-xs text-destructive">{error}</p>
                    )}
                  </div>
                );
              })}
            </>
          )}

          {!loading && dag && !useSchemaFields && !useTypedFields && (
            <>
              {rawParams.map((param, index) => (
                <div
                  key={`${param.Name ?? 'pos'}-${index}`}
                  className="space-y-2"
                >
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
                    value={rawParams[index]?.Value ?? ''}
                    readOnly={paramsReadOnly}
                    disabled={paramsReadOnly || submitting}
                    className={
                      paramsReadOnly ? 'bg-muted cursor-not-allowed' : ''
                    }
                    onInput={(event) => autoGrowTextarea(event.currentTarget)}
                    onChange={(event) => {
                      if (paramsReadOnly) {
                        return;
                      }
                      setRawParams((prev) =>
                        prev.map((item, itemIndex) =>
                          itemIndex === index
                            ? { ...item, Value: event.target.value }
                            : item
                        )
                      );
                      setSubmitError(null);
                    }}
                  />
                </div>
              ))}
            </>
          )}
        </div>

        <DialogFooter>
          <Button
            ref={cancelButtonRef}
            variant="ghost"
            onClick={dismissModal}
            disabled={submitting}
          >
            <X className="h-4 w-4" />
            Cancel
          </Button>
          <Button
            onClick={() => void handleSubmit()}
            disabled={loading || !!loadError || submitting}
          >
            {enqueue ? (
              <>
                <ListPlus className="h-4 w-4" />
                {submitting ? 'Enqueuing...' : 'Enqueue'}
              </>
            ) : (
              <>
                <Play className="h-4 w-4" />
                {submitting ? 'Starting...' : 'Start'}
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function renderTypedField({
  field,
  disabled,
  onChange,
}: {
  field: ParamField;
  disabled: boolean;
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
        <SelectTrigger id={`param-${field.key}`} className={controlClass}>
          <SelectValue placeholder={placeholder} />
        </SelectTrigger>
        <SelectContent>
          {!field.required && !field.hasDefault && (
            <SelectItem value="__unset__">Not set</SelectItem>
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
        <Input
          id={`param-${field.key}`}
          type="number"
          className={controlClass}
          min={field.minimum}
          max={field.maximum}
          step={field.type === ParamDefType.integer ? 1 : 'any'}
          value={field.hasValue ? field.value : ''}
          placeholder={field.hasValue ? undefined : 'Not set'}
          disabled={disabled}
          onChange={(event) =>
            onChange({
              value: event.target.value,
              hasValue: event.target.value !== '',
            })
          }
        />
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
            <SelectTrigger id={`param-${field.key}`} className={controlClass}>
              <SelectValue placeholder="Not set" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__unset__">Not set</SelectItem>
              <SelectItem value="true">true</SelectItem>
              <SelectItem value="false">false</SelectItem>
            </SelectContent>
          </Select>
        );
      }
      return (
        <div className="flex items-center gap-3 rounded-md border border-border px-3 py-2">
          <Switch
            checked={field.value === 'true'}
            disabled={disabled}
            onCheckedChange={(checked) =>
              onChange({ value: checked ? 'true' : 'false', hasValue: true })
            }
          />
          <span className="text-sm text-muted-foreground">
            {field.value === 'true' ? 'true' : 'false'}
          </span>
        </div>
      );

    case ParamDefType.string:
    default:
      return (
        <Textarea
          id={`param-${field.key}`}
          className={`${controlClass} h-7 min-h-7 py-1`}
          rows={1}
          ref={(element) => {
            if (element) {
              autoGrowTextarea(element);
            }
          }}
          value={field.hasValue ? field.value : ''}
          placeholder={field.hasValue ? undefined : 'Not set'}
          disabled={disabled}
          onInput={(event) => autoGrowTextarea(event.currentTarget)}
          onChange={(event) =>
            onChange({
              value: event.target.value,
              hasValue: true,
            })
          }
        />
      );
  }
}

export default StartDAGModal;
