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
import {
  AlertTriangle,
  ListPlus,
  Maximize2,
  Minimize2,
  Play,
  X,
} from 'lucide-react';
import React from 'react';

import { components, RuntimeProfileStatus } from '../../../../api/v1/schema';
import { DAGParamsFields, useDAGParamsForm } from './DAGParamsForm';
import { SlotDatesPreview } from './SlotDatesPreview';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type DAGLike =
  | components['schemas']['DAG']
  | components['schemas']['DAGDetails'];

const NO_PARAM_HINTS: Record<string, string> = {};

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
  const paramsForm = useDAGParamsForm(dag, visible);
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
  const parameterCount = paramsForm.count;

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
    runConfig?: { disableRunIdEdit?: boolean };
  };

  const paramsReadOnly = paramsForm.locked;
  const runIdReadOnly = dagWithRunConfig?.runConfig?.disableRunIdEdit ?? false;
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
  }, [defaultProfile, visible]);

  const cancelButtonRef = React.useRef<HTMLButtonElement>(null);

  const handleSubmit = React.useCallback(async () => {
    if (!dag || loading || !!loadError || submitting || !!datesBlocked) {
      return;
    }

    if (!paramsForm.validate()) {
      setSubmitError('Fix the highlighted parameter errors before submitting.');
      return;
    }
    const paramsPayload = paramsForm.serialize();

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
    paramsForm,
    submitting,
    datesBlocked,
    profileSelection,
    slotSelection,
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

                  {!loading && dag && (
                    <DAGParamsFields
                      form={paramsForm}
                      slotHints={paramHints}
                      disabled={submitting}
                      onEdit={() => setSubmitError(null)}
                    />
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

export default StartDAGModal;
