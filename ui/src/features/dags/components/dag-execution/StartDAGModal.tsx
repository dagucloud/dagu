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
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
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
import type { FormContextType, RJSFSchema, UiSchema } from '@rjsf/utils';
import validator from '@rjsf/validator-ajv8';
import {
  AlertTriangle,
  ListPlus,
  Maximize2,
  Minimize2,
  Play,
  X,
} from 'lucide-react';
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
  type ParamSchemaUiSchema,
  stringifyParamSchemaFormData,
} from './paramSchemaForm';
import { ParamLabel } from './ParamLabel';
import { paramFormTemplates } from './paramFormTemplates';
import { paramFormWidgets } from './paramFormWidgets';
import { SlotDatesPreview } from './SlotDatesPreview';
import { autoGrowTextarea } from './textareaAutoGrow';
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
  /** The DAG's file, which previews the dates of the chosen slot. */
  fileName?: string;
  loading?: boolean;
  loadError?: string | null;
  dismissModal: () => void;
  onSubmit: (
    params: string,
    dagRunId?: string,
    immediate?: boolean,
    profile?: string,
    noReuse?: boolean,
    scheduleTime?: string
  ) => Promise<void> | void;
  action?: 'start' | 'enqueue';
  profiles?: components['schemas']['RuntimeProfileResponse'][];
  profilesLoading?: boolean;
  defaultProfile?: string;
  defaultProfileLoading?: boolean;
};

const DAG_DEFAULT_PROFILE_VALUE = '__dag_default__';
const NO_PROFILE_VALUE = '__none__';
const NO_SLOT_VALUE = '__no_slot__';
const OTHER_SLOT_VALUE = '__other_slot__';

type RecentFire = components['schemas']['RecentFire'];

// formatRecentFire shows a fire in the zone of its schedule, as the API
// returns it, e.g. "2026-11-11 02:00 (Asia/Tokyo)".
function formatRecentFire(fire: RecentFire): string {
  return `${fire.time.slice(0, 10)} ${fire.time.slice(11, 16)} (${fire.timezone})`;
}

const NO_PARAM_HINTS: Record<string, string> = {};

// withPlaceholders shows each hint as the placeholder of its param's field.
function withPlaceholders(
  uiSchema: ParamSchemaUiSchema,
  hints: Record<string, string>
): ParamSchemaUiSchema {
  const result = { ...uiSchema };
  for (const [name, hint] of Object.entries(hints)) {
    result[name] = { ...result[name], 'ui:placeholder': hint };
  }
  return result;
}

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
      title: def.title,
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
  fileName,
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
  const [isFullscreen, setIsFullscreen] = React.useState(false);
  const forceEnqueue = action === 'enqueue';
  // A run waits in the queue unless the user unchecks Enqueue.
  const [enqueue, setEnqueue] = React.useState(true);
  const [noReuse, setNoReuse] = React.useState(false);
  const [slotSelection, setSlotSelection] = React.useState(NO_SLOT_VALUE);
  const [customSlot, setCustomSlot] = React.useState('');
  const remoteNode = useRemoteNode();
  // The preview is read only for a DAG that declares dates.
  const showDatesPreview =
    !!fileName && (dagDetails?.dateNames?.length ?? 0) > 0;
  // The reason the chosen slot's dates cannot resolve, as the preview last
  // reported it. It holds the start only while the preview is shown.
  const [previewBlock, setPreviewBlock] = React.useState<string | null>(null);
  const datesBlocked = showDatesPreview ? previewBlock : null;
  // The values params take from the slot, shown as their placeholders: the
  // param's eval still decides its value when the run starts.
  const [previewParams, setPreviewParams] =
    React.useState<Record<string, string>>(NO_PARAM_HINTS);
  const paramHints = showDatesPreview ? previewParams : NO_PARAM_HINTS;
  const previewSlot =
    slotSelection === OTHER_SLOT_VALUE
      ? customSlot.trim() || undefined
      : slotSelection === NO_SLOT_VALUE
        ? undefined
        : slotSelection;
  const recentFires = React.useMemo(
    () => dagDetails?.recentFires ?? [],
    [dagDetails]
  );
  const isBuild = dagDetails?.type === 'build';
  const activeProfiles = React.useMemo(
    () =>
      profiles.filter(
        (profile) => profile.status === RuntimeProfileStatus.active
      ),
    [profiles]
  );
  const hasDefaultProfile = defaultProfile !== '';
  // The listed slots come from the DAG's default profile. Another profile
  // can have schedules of its own, which decide the run's window.
  const defaultProfileName = defaultProfile ?? '';
  const selectedProfileName =
    profileSelection === DAG_DEFAULT_PROFILE_VALUE
      ? defaultProfileName
      : profileSelection === NO_PROFILE_VALUE
        ? ''
        : profileSelection;
  const slotsFollowOtherProfile =
    recentFires.length > 0 && selectedProfileName !== defaultProfileName;
  const showProfileSelector =
    defaultProfileLoading ||
    profilesLoading ||
    hasDefaultProfile ||
    activeProfiles.length > 0;
  const parameterCount = useSchemaFields
    ? Object.keys(paramSchema.properties ?? {}).length
    : useTypedFields
      ? paramDefs.length
      : initialRawParams.length;

  React.useEffect(() => {
    if (visible) {
      setIsFullscreen(false);
    }
  }, [visible]);

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

  // A workflow without parameters has none to lock, so it gets no note.
  const paramsReadOnly =
    (dagWithRunConfig?.runConfig?.disableParamEdit ?? false) &&
    parameterCount > 0;
  const runIdReadOnly = dagWithRunConfig?.runConfig?.disableRunIdEdit ?? false;
  const schemaFormUiSchema = React.useMemo<
    UiSchema<SchemaFormData> | undefined
  >(
    () =>
      paramSchema
        ? ({
            ...withPlaceholders(
              buildParamSchemaUiSchema(paramSchema),
              paramHints
            ),
            'ui:order': paramDefs
              .flatMap(({ name }) => (name ? [name] : []))
              .concat('*'),
            'ui:submitButtonOptions': { norender: true },
          } as UiSchema<SchemaFormData>)
        : undefined,
    [paramDefs, paramSchema, paramHints]
  );
  const schemaFormRef = React.useRef<RJSFForm<
    SchemaFormData,
    RJSFSchema,
    FormContextType
  > | null>(null);
  const submitErrorRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (submitError) {
      submitErrorRef.current?.focus();
    }
  }, [submitError]);

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
    setEnqueue(true);
    setNoReuse(false);
    setSlotSelection(NO_SLOT_VALUE);
    setCustomSlot('');
  }, [
    defaultProfile,
    visible,
    initialSchemaFormData,
    initialTypedFields,
    initialRawParams,
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
    if (!dag || loading || !!loadError || submitting || !!datesBlocked) {
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

    let scheduleTime: string | undefined;
    if (slotSelection === OTHER_SLOT_VALUE) {
      scheduleTime = customSlot.trim();
      if (!scheduleTime) {
        setSubmitError('Enter the scheduled time, or choose None.');
        return;
      }
    } else if (slotSelection !== NO_SLOT_VALUE) {
      scheduleTime = slotSelection;
    }

    setSubmitting(true);
    setSubmitError(null);
    try {
      const profileOverride = profileOverrideForSelection(
        profileSelection,
        hasDefaultProfile
      );
      if (scheduleTime) {
        await onSubmit(
          paramsPayload,
          dagRunId || undefined,
          !enqueue,
          profileOverride,
          isBuild ? noReuse : undefined,
          scheduleTime
        );
      } else if (isBuild) {
        await onSubmit(
          paramsPayload,
          dagRunId || undefined,
          !enqueue,
          profileOverride,
          noReuse
        );
      } else if (profileOverride === undefined) {
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
    customSlot,
    dag,
    dagRunId,
    dismissModal,
    enqueue,
    hasDefaultProfile,
    loadError,
    loading,
    isBuild,
    noReuse,
    onSubmit,
    rawParams,
    schemaFormData,
    useSchemaFields,
    submitting,
    datesBlocked,
    profileSelection,
    slotSelection,
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

  // Without a note, an error or a parameter, the body would be an empty band.
  const hasBody =
    isFullscreen ||
    paramsReadOnly ||
    runIdReadOnly ||
    !!loadError ||
    !!submitError ||
    loading ||
    parameterCount > 0;

  return (
    <Dialog open={visible} onOpenChange={(open) => !open && dismissModal()}>
      <DialogContent
        fullscreen={isFullscreen}
        className={
          isFullscreen
            ? 'flex flex-col'
            : 'flex max-h-[90dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-[760px] max-sm:left-0 max-sm:top-0 max-sm:h-[100dvh] max-sm:max-h-[100dvh] max-sm:max-w-none max-sm:translate-x-0 max-sm:translate-y-0 max-sm:rounded-none'
        }
      >
        <DialogHeader className="min-h-12 shrink-0 border-b border-border px-6 py-3 pr-24">
          <DialogTitle>
            {forceEnqueue ? (
              <I18nText text={'Enqueue workflow'} />
            ) : (
              <I18nText text={'Start workflow'} />
            )}
          </DialogTitle>
          <DialogDescription className="sr-only">
            <I18nText text={'Configure the DAG run before submitting it.'} />
          </DialogDescription>
        </DialogHeader>

        <I18nProps>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="absolute right-12 top-3 hidden sm:inline-flex"
            aria-label={isFullscreen ? 'Restore dialog' : 'Maximize dialog'}
            aria-pressed={isFullscreen}
            onClick={() => setIsFullscreen((current) => !current)}
          >
            {isFullscreen ? <Minimize2 /> : <Maximize2 />}
          </Button>
        </I18nProps>

        <I18nProps>
          <fieldset
            aria-label="Run settings"
            className="shrink-0 border-b border-border px-6 py-3"
          >
            <div
              className={
                !forceEnqueue && showProfileSelector
                  ? 'grid gap-4 sm:grid-cols-[minmax(120px,0.55fr)_minmax(240px,1.45fr)_minmax(180px,1fr)]'
                  : 'grid gap-4 sm:grid-cols-2'
              }
            >
              {!forceEnqueue && (
                <div className="space-y-1.5">
                  {/* Keeps the row of the labels of the other fields; the
                      checkbox label says Enqueue once. */}
                  <span aria-hidden="true" className="block text-sm">
                    &nbsp;
                  </span>
                  <div className="flex h-7 items-center gap-2">
                    <Checkbox
                      id="enqueue"
                      checked={enqueue}
                      onCheckedChange={(checked) =>
                        setEnqueue(checked as boolean)
                      }
                      disabled={loading || submitting}
                    />
                    <Label htmlFor="enqueue" className="cursor-pointer">
                      <I18nText text={'Enqueue'} />
                    </Label>
                  </div>
                </div>
              )}

              <div className="space-y-1.5">
                <Label htmlFor="dagRun-id">
                  <I18nText text={'Run ID (optional)'} />
                </Label>
                <I18nProps>
                  <Input
                    id="dagRun-id"
                    placeholder="Enter a custom run ID"
                    value={dagRunId}
                    readOnly={runIdReadOnly}
                    disabled={runIdReadOnly || loading || submitting}
                    className={
                      runIdReadOnly
                        ? 'h-7 cursor-not-allowed bg-muted px-2'
                        : 'h-7 px-2'
                    }
                    onChange={(event) => {
                      if (!runIdReadOnly) {
                        setDAGRunId(event.target.value);
                      }
                    }}
                  />
                </I18nProps>
              </div>

              {recentFires.length > 0 && (
                <div className="space-y-1.5">
                  <Label htmlFor="scheduled-at">
                    <I18nText text={'Scheduled at (optional)'} />
                  </Label>
                  <Select
                    value={slotSelection}
                    disabled={loading || submitting}
                    onValueChange={setSlotSelection}
                  >
                    <SelectTrigger
                      id="scheduled-at"
                      className="h-7 w-full px-2 py-1"
                    >
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={NO_SLOT_VALUE}>
                        <I18nText text={'None (run now)'} />
                      </SelectItem>
                      {recentFires.map((fire) => (
                        <SelectItem key={fire.time} value={fire.time}>
                          {formatRecentFire(fire)}
                        </SelectItem>
                      ))}
                      <SelectItem value={OTHER_SLOT_VALUE}>
                        <I18nText text={'Other time'} />
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  {slotSelection === OTHER_SLOT_VALUE && (
                    <I18nProps>
                      <Input
                        id="scheduled-at-custom"
                        aria-label="Scheduled time"
                        placeholder="latest, 2026-11-09, 2026-11-09T02:00, or RFC 3339"
                        value={customSlot}
                        disabled={loading || submitting}
                        className="h-7 px-2"
                        onChange={(event) => setCustomSlot(event.target.value)}
                      />
                    </I18nProps>
                  )}
                  {slotSelection !== NO_SLOT_VALUE && (
                    <p className="text-xs text-muted-foreground">
                      <I18nText
                        text={
                          'The run gets the schedule time and window of this slot.'
                        }
                      />
                    </p>
                  )}
                  {slotsFollowOtherProfile && (
                    <p className="text-xs text-muted-foreground">
                      <I18nText
                        text={
                          "The listed times follow the DAG's default profile. The run's window follows the selected profile."
                        }
                      />
                    </p>
                  )}
                </div>
              )}

              {fileName && showDatesPreview && (
                <div className="space-y-1.5 sm:col-span-full">
                  <SlotDatesPreview
                    fileName={fileName}
                    remoteNode={remoteNode}
                    scheduledAt={previewSlot}
                    profile={profileOverrideForSelection(
                      profileSelection,
                      hasDefaultProfile
                    )}
                    onBlocked={setPreviewBlock}
                    onParams={setPreviewParams}
                  />
                </div>
              )}

              {showProfileSelector && (
                <div className="space-y-1.5">
                  <Label htmlFor="runtime-profile">
                    <I18nText text={'Profile'} />
                  </Label>
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
                    <SelectTrigger
                      id="runtime-profile"
                      className="h-7 w-full px-2 py-1"
                    >
                      <I18nProps>
                        <SelectValue placeholder="No profile" />
                      </I18nProps>
                    </SelectTrigger>
                    <SelectContent>
                      {hasDefaultProfile && (
                        <SelectItem value={DAG_DEFAULT_PROFILE_VALUE}>
                          <span className="flex w-full items-center justify-between gap-3">
                            <span>
                              <I18nText text={'DAG default'} />
                            </span>
                            <span className="truncate text-xs text-muted-foreground">
                              {defaultProfile}
                            </span>
                          </span>
                        </SelectItem>
                      )}
                      <SelectItem value={NO_PROFILE_VALUE}>
                        <I18nText text={'No profile'} />
                      </SelectItem>
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
                                    <I18nText text={'Protected'} />
                                  </Badge>
                                )}
                                {protectedUnavailable && (
                                  <Badge
                                    variant="secondary"
                                    className="h-4 px-1.5 text-[10px]"
                                  >
                                    <I18nText text={'Admin'} />
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
            </div>

            {isBuild && (
              <div className="mt-4 flex items-start space-x-2 border-t border-border pt-4">
                <Checkbox
                  id="no-reuse"
                  checked={noReuse}
                  onCheckedChange={(checked) => setNoReuse(checked as boolean)}
                  disabled={loading || submitting}
                />
                <div className="space-y-0.5">
                  <Label htmlFor="no-reuse" className="cursor-pointer">
                    <I18nText text={'Disable reuse for this run'} />
                  </Label>
                  <p className="text-xs text-muted-foreground">
                    <I18nText
                      text={
                        'Eligible steps execute and replace their materializations only after success.'
                      }
                    />
                  </p>
                </div>
              </div>
            )}
          </fieldset>
        </I18nProps>

        {hasBody && (
          <div
            data-testid="start-dag-body"
            className="min-h-0 flex-1 overflow-y-auto px-6 py-4"
          >
            <div className="space-y-4">
              {(paramsReadOnly || runIdReadOnly) && (
                <div className="rounded-md border border-warning/30 bg-warning-muted p-3">
                  <p className="text-sm text-warning">
                    <strong>
                      <I18nText text={'Note:'} />
                    </strong>{' '}
                    <I18nText text={'This DAG has restrictions:'} />
                    {paramsReadOnly && runIdReadOnly && (
                      <span>
                        {' '}
                        <I18nText
                          text={
                            'Parameter editing and custom run IDs are disabled.'
                          }
                        />
                      </span>
                    )}
                    {paramsReadOnly && !runIdReadOnly && (
                      <span>
                        {' '}
                        <I18nText text={'Parameter editing is disabled.'} />
                      </span>
                    )}
                    {!paramsReadOnly && runIdReadOnly && (
                      <span>
                        {' '}
                        <I18nText text={'Custom run IDs are disabled.'} />
                      </span>
                    )}
                  </p>
                </div>
              )}

              {(loadError || submitError) && (
                <Alert ref={submitErrorRef} variant="destructive" tabIndex={-1}>
                  <AlertTriangle className="h-4 w-4" />
                  <AlertDescription>
                    {loadError ?? submitError}
                  </AlertDescription>
                </Alert>
              )}

              {(loading || parameterCount > 0) && (
                <section
                  aria-labelledby="dag-parameters-heading"
                  className="space-y-4"
                >
                  <div className="flex items-center gap-2">
                    <h3 id="dag-parameters-heading" className="font-semibold">
                      <I18nText text={'Parameters'} />
                    </h3>
                    <Badge variant="secondary" className="font-normal">
                      {parameterCount}{' '}
                      {parameterCount === 1 ? (
                        <I18nText text={'field'} />
                      ) : (
                        <I18nText text={'fields'} />
                      )}
                    </Badge>
                  </div>

                  {loading && (
                    <div className="rounded-md border border-border bg-muted/40 p-3 text-sm text-muted-foreground">
                      <I18nText text={'Loading workflow...'} />
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
                      templates={paramFormTemplates}
                      widgets={paramFormWidgets}
                      disabled={paramsReadOnly || submitting}
                      readonly={paramsReadOnly}
                      noHtml5Validate
                      showErrorList={false}
                      onChange={(event: IChangeEvent<SchemaFormData>) => {
                        setSchemaFormData(
                          (event.formData ?? {}) as SchemaFormData
                        );
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
                        const label =
                          field.title || field.name || `Parameter ${index + 1}`;
                        const error = fieldErrors[field.key];
                        const descriptionId = `param-${field.key}-description`;
                        const errorId = `param-${field.key}-error`;
                        const describedBy =
                          [field.description && descriptionId, error && errorId]
                            .filter(Boolean)
                            .join(' ') || undefined;
                        const disableField = paramsReadOnly || submitting;
                        const showUnsetButton =
                          !field.required &&
                          !field.hasDefault &&
                          field.hasValue;

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
                                  onClick={() =>
                                    handleTypedFieldChange(field.key, {
                                      hasValue: false,
                                      value: '',
                                    })
                                  }
                                >
                                  <I18nText text={'Unset'} />
                                </Button>
                              )}
                            </div>

                            {field.description && (
                              <p
                                id={descriptionId}
                                className="text-xs text-muted-foreground"
                              >
                                {field.description}
                              </p>
                            )}

                            {renderTypedField({
                              field,
                              hint: field.name
                                ? paramHints[field.name]
                                : undefined,
                              disabled: disableField,
                              describedBy,
                              invalid: !!error,
                              onChange: (patch) =>
                                handleTypedFieldChange(field.key, patch),
                            })}

                            {error && (
                              <p
                                id={errorId}
                                className="text-xs text-destructive"
                              >
                                {error}
                              </p>
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
                              paramsReadOnly
                                ? 'bg-muted cursor-not-allowed'
                                : ''
                            }
                            onInput={(event) =>
                              autoGrowTextarea(event.currentTarget)
                            }
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
                </section>
              )}
            </div>
          </div>
        )}

        <DialogFooter className="shrink-0 border-t border-border px-6 py-4">
          <Button
            ref={cancelButtonRef}
            variant="ghost"
            onClick={dismissModal}
            disabled={submitting}
          >
            <X className="h-4 w-4" />
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            variant="primary"
            onClick={() => void handleSubmit()}
            disabled={loading || !!loadError || submitting || !!datesBlocked}
          >
            {enqueue ? (
              <>
                <ListPlus className="h-4 w-4" />
                {submitting ? (
                  <I18nText text={'Enqueuing...'} />
                ) : (
                  <I18nText text={'Enqueue'} />
                )}
              </>
            ) : (
              <>
                <Play className="h-4 w-4" />
                {submitting ? (
                  <I18nText text={'Starting...'} />
                ) : (
                  <I18nText text={'Start'} />
                )}
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

export default StartDAGModal;
