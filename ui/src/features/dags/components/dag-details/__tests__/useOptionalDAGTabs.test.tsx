// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RuntimeProfileStatus } from '@/api/v1/schema';
import { useOptionalDAGTabs } from '../useOptionalDAGTabs';

const state = vi.hoisted(() => ({
  canManageProfiles: true,
  profiles: undefined as unknown,
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanManageProfiles: () => state.canManageProfiles,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: state.profiles, mutate: vi.fn() }),
}));

function profile(name: string, status = RuntimeProfileStatus.active) {
  return { id: name, name, status };
}

function tabs(activeTab = 'status') {
  return renderHook(() => useOptionalDAGTabs(activeTab)).result.current;
}

describe('useOptionalDAGTabs', () => {
  beforeEach(() => {
    state.canManageProfiles = true;
    state.profiles = { profiles: [] };
  });

  it('leaves out the Settings tab when there is no profile to choose', () => {
    expect(tabs()).toEqual({ settings: false });
  });

  // "No profile" and the one profile are two different runtime choices.
  it('shows the Settings tab as soon as one profile is active', () => {
    state.profiles = { profiles: [profile('prod')] };
    expect(tabs().settings).toBe(true);

    state.profiles = { profiles: [profile('prod'), profile('dev')] };
    expect(tabs().settings).toBe(true);

    // A profile that is not active is not a choice.
    state.profiles = {
      profiles: [profile('old', RuntimeProfileStatus.disabled)],
    };
    expect(tabs().settings).toBe(false);
  });

  it('keeps the open tab so a page reached by its URL still shows it', () => {
    expect(tabs('settings').settings).toBe(true);
  });
});
