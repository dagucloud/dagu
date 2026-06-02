// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import LoadingIndicator from '@/components/ui/loading-indicator';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanManageProfiles, useIsAdmin } from '@/contexts/AuthContext';
import { useClient, useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { AlertTriangle, Save, X } from 'lucide-react';
import React from 'react';
import { RuntimeProfileStatus } from '../../../../api/v1/schema';

type Props = {
  fileName: string;
};

const NO_PROFILE_VALUE = '__none__';

function DAGSettingsTab({ fileName }: Props) {
  const client = useClient();
  const appBarContext = React.useContext(AppBarContext);
  const remoteNode = appBarContext.selectedRemoteNode || 'local';
  const canManageProfiles = useCanManageProfiles();
  const canUseProtectedProfiles = useIsAdmin();
  const [selectedProfile, setSelectedProfile] = React.useState('');
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const settingsQuery = React.useMemo(
    () =>
      whenEnabled(!!fileName, {
        params: {
          path: { fileName },
          query: { remoteNode },
        },
      }),
    [fileName, remoteNode]
  );
  const {
    data: settings,
    isLoading: settingsLoading,
    mutate: mutateSettings,
  } = useQuery('/dags/{fileName}/settings', settingsQuery);

  const profilesQuery = React.useMemo(
    () =>
      whenEnabled(canManageProfiles, {
        params: {
          query: { remoteNode },
        },
      }),
    [canManageProfiles, remoteNode]
  );
  const { data: profilesData, isLoading: profilesLoading } = useQuery(
    '/profiles',
    profilesQuery
  );

  const activeProfiles = React.useMemo(
    () =>
      (profilesData?.profiles ?? []).filter(
        (profile) => profile.status === RuntimeProfileStatus.active
      ),
    [profilesData?.profiles]
  );
  const currentProfile = settings?.profile ?? '';

  React.useEffect(() => {
    setSelectedProfile(currentProfile);
  }, [currentProfile]);

  const selectedProfileRecord = activeProfiles.find(
    (profile) => profile.name === selectedProfile
  );
  const selectedProfileUnavailable =
    selectedProfile !== '' && !selectedProfileRecord;
  const selectedProtectedUnavailable =
    !!selectedProfileRecord?.protected && !canUseProtectedProfiles;
  const hasChanges = selectedProfile !== currentProfile;
  const canSave =
    canManageProfiles &&
    hasChanges &&
    !saving &&
    !profilesLoading &&
    !selectedProfileUnavailable &&
    !selectedProtectedUnavailable;

  const save = React.useCallback(async () => {
    if (!canSave) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      if (selectedProfile) {
        const { error } = await client.PUT('/dags/{fileName}/settings', {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
          body: {
            profile: selectedProfile,
          },
        });
        if (error) {
          throw new Error(error.message || 'Failed to save settings.');
        }
      } else {
        const { error } = await client.DELETE('/dags/{fileName}/settings', {
          params: {
            path: { fileName },
            query: { remoteNode },
          },
        });
        if (error) {
          throw new Error(error.message || 'Failed to clear settings.');
        }
      }
      await mutateSettings();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save settings.');
    } finally {
      setSaving(false);
    }
  }, [canSave, client, fileName, mutateSettings, remoteNode, selectedProfile]);

  if (settingsLoading) {
    return (
      <div className="flex min-h-40 items-center justify-center">
        <LoadingIndicator />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {error && (
        <Alert variant="destructive">
          <AlertTriangle className="h-4 w-4" />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="rounded-md border border-border p-4">
        <div className="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
          <div className="w-full max-w-md space-y-2">
            <Label htmlFor="dag-default-profile">Default profile</Label>
            {canManageProfiles ? (
              <Select
                value={selectedProfile || NO_PROFILE_VALUE}
                disabled={saving || profilesLoading}
                onValueChange={(value) =>
                  setSelectedProfile(value === NO_PROFILE_VALUE ? '' : value)
                }
              >
                <SelectTrigger id="dag-default-profile" className="w-full">
                  <SelectValue placeholder="No profile" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NO_PROFILE_VALUE}>No profile</SelectItem>
                  {selectedProfileUnavailable && (
                    <SelectItem value={selectedProfile} disabled>
                      <span className="flex w-full items-center justify-between gap-3">
                        <span>{selectedProfile}</span>
                        <Badge
                          variant="secondary"
                          className="h-4 px-1.5 text-[10px]"
                        >
                          Restricted
                        </Badge>
                      </span>
                    </SelectItem>
                  )}
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
                          {profile.protected && (
                            <Badge
                              variant="outline"
                              className="h-4 px-1.5 text-[10px]"
                            >
                              Protected
                            </Badge>
                          )}
                        </span>
                      </SelectItem>
                    );
                  })}
                </SelectContent>
              </Select>
            ) : (
              <div className="flex min-h-10 items-center rounded-md border border-border px-3 text-sm">
                {currentProfile || 'No profile'}
              </div>
            )}
          </div>

          {canManageProfiles && (
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={saving || !hasChanges}
                onClick={() => setSelectedProfile(currentProfile)}
              >
                <X className="mr-2 h-4 w-4" />
                Reset
              </Button>
              <Button type="button" disabled={!canSave} onClick={save}>
                <Save className="mr-2 h-4 w-4" />
                Save
              </Button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export default DAGSettingsTab;
