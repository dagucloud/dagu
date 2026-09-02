// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Loader2, RefreshCw, Save } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { components, RuntimeProfileStatus } from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { useClient } from '../../../../hooks/api';
import {
  findUnavailableAllowedProfiles,
  updateAllowedProfiles,
} from './webhookProfileSelection';
import { I18nText } from '@/i18n/I18nText';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type RuntimeProfile = components['schemas']['RuntimeProfileResponse'];
type WebhookDetails = components['schemas']['WebhookDetails'];

interface WebhookProfileSelectionCardProps {
  fileName: string;
  isAdmin: boolean;
  remoteNode: string;
  webhook: WebhookDetails;
  onActiveProfileNamesChange: (profileNames: string[]) => void;
  onWebhookChange: (webhook: WebhookDetails) => void;
}

function WebhookProfileSelectionCard({
  fileName,
  isAdmin,
  remoteNode,
  webhook,
  onActiveProfileNamesChange,
  onWebhookChange,
}: WebhookProfileSelectionCardProps) {
  const client = useClient();
  const configuredAllowedProfiles = webhook.profileSelection.allowedProfiles;
  const configuredAllowedProfilesKey = JSON.stringify(
    [...configuredAllowedProfiles].sort()
  );
  const configuredAllowedProfilesRef = useRef(configuredAllowedProfiles);
  configuredAllowedProfilesRef.current = configuredAllowedProfiles;
  const [runtimeProfiles, setRuntimeProfiles] = useState<RuntimeProfile[]>([]);
  const [draftAllowedProfiles, setDraftAllowedProfiles] = useState<string[]>(
    configuredAllowedProfiles
  );
  const [profilesLoading, setProfilesLoading] = useState(false);
  const [profilesLoadFailed, setProfilesLoadFailed] = useState(false);
  const [profileSelectionSaving, setProfileSelectionSaving] = useState(false);
  const [profileSelectionError, setProfileSelectionError] = useState<
    string | null
  >(null);
  const [profilesReloadKey, setProfilesReloadKey] = useState(0);

  useEffect(() => {
    setDraftAllowedProfiles(configuredAllowedProfilesRef.current);
  }, [configuredAllowedProfilesKey]);

  useEffect(() => {
    if (!isAdmin) {
      setRuntimeProfiles([]);
      onActiveProfileNamesChange([]);
      setProfilesLoading(false);
      setProfilesLoadFailed(false);
      setProfileSelectionError(null);
      return;
    }

    let cancelled = false;
    const fetchProfiles = async () => {
      setProfilesLoading(true);
      setProfilesLoadFailed(false);
      setRuntimeProfiles([]);
      onActiveProfileNamesChange([]);
      setProfileSelectionError(null);
      try {
        const { data, error } = await client.GET('/profiles', {
          params: { query: { remoteNode } },
        });
        if (cancelled) return;
        if (error || !data) {
          throw new Error(error?.message || 'Failed to load runtime profiles');
        }
        const activeProfiles = data.profiles.filter(
          (profile) => profile.status === RuntimeProfileStatus.active
        );
        setRuntimeProfiles(activeProfiles);
        onActiveProfileNamesChange(
          activeProfiles.map((profile) => profile.name)
        );
      } catch (error) {
        if (cancelled) return;
        onActiveProfileNamesChange([]);
        setProfileSelectionError(
          error instanceof Error
            ? error.message
            : 'Failed to load runtime profiles'
        );
        setProfilesLoadFailed(true);
      } finally {
        if (!cancelled) {
          setProfilesLoading(false);
        }
      }
    };

    void fetchProfiles();
    return () => {
      cancelled = true;
    };
  }, [
    client,
    isAdmin,
    onActiveProfileNamesChange,
    profilesReloadKey,
    remoteNode,
    webhook.id,
  ]);

  const handleAllowedProfileChange = (
    profileName: string,
    checked: boolean
  ) => {
    setDraftAllowedProfiles((current) =>
      updateAllowedProfiles(current, profileName, checked)
    );
  };

  const handleSave = async () => {
    try {
      setProfileSelectionSaving(true);
      setProfileSelectionError(null);
      const { data, error } = await client.PUT(
        '/dags/{fileName}/webhook/profile-selection',
        {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
          body: { allowedProfiles: draftAllowedProfiles },
        }
      );
      if (error || !data) {
        throw new Error(error?.message || 'Failed to update profile selection');
      }
      onWebhookChange(data);
    } catch (error) {
      setProfileSelectionError(
        error instanceof Error
          ? error.message
          : 'Failed to update profile selection'
      );
    } finally {
      setProfileSelectionSaving(false);
    }
  };

  const profileSelectionChanged =
    JSON.stringify([...draftAllowedProfiles].sort()) !==
    JSON.stringify([...configuredAllowedProfiles].sort());
  const unavailableAllowedProfiles = findUnavailableAllowedProfiles(
    draftAllowedProfiles,
    runtimeProfiles.map((profile) => profile.name)
  );

  return (
    <Card className="gap-0 py-0">
      <CardHeader className="pb-3 px-4 pt-3">
        <CardTitle className="text-sm">
          <I18nText text={'Runtime profile selection'} />
        </CardTitle>
        <CardDescription className="text-xs">
          <I18nTemplate
            text="Allow callers to select an approved profile with {header}. Without the header, the DAG's default profile resolution is used."
            values={{
              header: (
                <code className="bg-accent px-1 rounded-md border">
                  X-Dagu-Profile
                </code>
              ),
            }}
          />
        </CardDescription>
      </CardHeader>
      <CardContent className="px-4 pb-3 pt-2 space-y-3">
        {(isAdmin || configuredAllowedProfiles.length > 0) && (
          <div className="rounded-md border bg-warning/10 px-3 py-2 text-xs text-muted-foreground">
            <I18nText
              text={
                'Anyone holding this webhook credential can run the DAG with every profile selected here.'
              }
            />
          </div>
        )}

        {profileSelectionError && (
          <div className="text-xs text-destructive">
            {profileSelectionError}
          </div>
        )}

        {isAdmin ? (
          <>
            {profilesLoading ? (
              <div className="flex items-center gap-2 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                <I18nText text={'Loading profiles...'} />
              </div>
            ) : (
              <div className="space-y-2">
                {profilesLoadFailed && (
                  <div className="flex items-center justify-between gap-2 rounded-md border p-3 text-xs text-muted-foreground">
                    <span>
                      <I18nText
                        text={
                          'Runtime profiles could not be loaded. Editing is disabled.'
                        }
                      />
                    </span>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setProfilesReloadKey((key) => key + 1)}
                    >
                      <RefreshCw className="mr-1 h-3.5 w-3.5" />
                      <I18nText text={'Retry'} />
                    </Button>
                  </div>
                )}
                <div className="space-y-2 rounded-md border p-3">
                  {runtimeProfiles.length === 0 &&
                  unavailableAllowedProfiles.length === 0 ? (
                    <div className="text-xs text-muted-foreground">
                      {profilesLoadFailed ? (
                        <I18nText text={'No configured profiles to display.'} />
                      ) : (
                        <I18nText
                          text={'No active runtime profiles are available.'}
                        />
                      )}
                    </div>
                  ) : (
                    runtimeProfiles.map((profile) => {
                      const inputId = `webhook-profile-${profile.id}`;
                      return (
                        <label
                          key={profile.id}
                          htmlFor={inputId}
                          className="flex cursor-pointer items-center gap-2 text-sm"
                        >
                          <Checkbox
                            id={inputId}
                            checked={draftAllowedProfiles.includes(
                              profile.name
                            )}
                            disabled={
                              profileSelectionSaving || profilesLoadFailed
                            }
                            onCheckedChange={(checked) =>
                              handleAllowedProfileChange(
                                profile.name,
                                checked === true
                              )
                            }
                          />
                          <span>{profile.name}</span>
                          {profile.protected && (
                            <Badge variant="outline" className="text-[10px]">
                              <I18nText text={'Protected'} />
                            </Badge>
                          )}
                        </label>
                      );
                    })
                  )}
                  {unavailableAllowedProfiles.map((profileName) => {
                    const inputId = `webhook-profile-unavailable-${profileName}`;
                    return (
                      <label
                        key={profileName}
                        htmlFor={inputId}
                        className="flex cursor-pointer items-center gap-2 text-sm"
                      >
                        <Checkbox
                          id={inputId}
                          checked
                          disabled={
                            profileSelectionSaving || profilesLoadFailed
                          }
                          onCheckedChange={(checked) =>
                            handleAllowedProfileChange(
                              profileName,
                              checked === true
                            )
                          }
                        />
                        <span>{profileName}</span>
                        <Badge variant="secondary" className="text-[10px]">
                          {profilesLoadFailed ? (
                            <I18nText text={'Status unknown'} />
                          ) : (
                            <I18nText text={'Unavailable'} />
                          )}
                        </Badge>
                      </label>
                    );
                  })}
                </div>
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={profileSelectionSaving || !profileSelectionChanged}
                onClick={() =>
                  setDraftAllowedProfiles(configuredAllowedProfiles)
                }
              >
                <I18nText text={'Reset'} />
              </Button>
              <Button
                size="sm"
                disabled={
                  profileSelectionSaving ||
                  profilesLoading ||
                  profilesLoadFailed ||
                  !profileSelectionChanged
                }
                onClick={handleSave}
              >
                {profileSelectionSaving ? (
                  <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" />
                ) : (
                  <Save className="h-3.5 w-3.5 mr-1" />
                )}
                <I18nText text={'Save profile selection'} />
              </Button>
            </div>
          </>
        ) : configuredAllowedProfiles.length > 0 ? (
          <div className="flex flex-wrap gap-2">
            {configuredAllowedProfiles.map((profileName) => (
              <Badge key={profileName} variant="secondary">
                {profileName}
              </Badge>
            ))}
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">
            <I18nText
              text={
                'Header-based profile selection is disabled. An administrator can configure it.'
              }
            />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

export default WebhookProfileSelectionCard;
