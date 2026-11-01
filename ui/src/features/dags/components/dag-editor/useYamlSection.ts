// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react';
import type { NdvScope } from './NodeDetailsFrame';

/** The state of the host: the URL flag `ndv` of the workspace. */
export interface YamlSectionHost {
  /** 'docked' is the YAML panel of the file; 'expanded' is a step's node view. */
  state: 'docked' | 'expanded' | null;
  onStateChange(state: 'docked' | 'expanded' | null): void;
  /** False for a hidden pane: it follows the state and never corrects it. Default true. */
  active?: boolean;
}

export interface YamlSection {
  /** What the YAML shows: the whole file (the YAML button) or one step. */
  yamlKind: NdvScope;
  setYamlKind(kind: NdvScope): void;
  /** True when the YAML panel or the node view is open. */
  sectionOpen: boolean;
  /** `source` is 'panel' when the YAML button opens the whole file. */
  setSectionOpen(open: boolean, source?: 'panel'): void;
}

/**
 * Which part of the YAML shows, and whether it is open. With a host, the
 * host owns whether the section is open, and its state follows what shows:
 * 'docked' for the file and 'expanded' for a step. The editor and the run
 * canvas of the workspace share this rule, so that one link opens the same
 * panel on both routes.
 */
export function useYamlSection(host?: YamlSectionHost): YamlSection {
  const [ownVisible, setOwnVisible] = useState(false);
  // It starts from the state that the host gives.
  const [yamlKind, setYamlKind] = useState<NdvScope>(
    host?.state === 'expanded' ? 'step' : 'file'
  );
  const sectionOpen = host ? host.state !== null : ownVisible;
  const onStateChange = host?.onStateChange;
  const hasHost = !!host;
  const stateRef = useRef(host?.state ?? null);
  stateRef.current = host?.state ?? null;
  const setSectionOpen = useCallback(
    (open: boolean, source?: 'panel') => {
      // The same call that opens the section says what it shows.
      if (open) setYamlKind(source === 'panel' ? 'file' : 'step');
      if (!hasHost) {
        setOwnVisible(open);
        return;
      }
      // The YAML panel opens docked, as the Workflow and Add step panels; a
      // step opens the node view, which fills the zone.
      const next = open ? (source === 'panel' ? 'docked' : 'expanded') : null;
      if (next !== stateRef.current) onStateChange?.(next);
    },
    [hasHost, onStateChange]
  );

  // A link that says otherwise than what shows is set right, except a host
  // that turns 'expanded': a link that opens a step shows the step. A pane
  // that is not active (hidden) never writes the state of the host: it
  // follows the state that another view of the page wrote.
  const hostState = host?.state ?? null;
  const hostActive = host?.active ?? true;
  const lastStateRef = useRef(hostState);
  useEffect(() => {
    const opensStep =
      hostState === 'expanded' && lastStateRef.current !== 'expanded';
    lastStateRef.current = hostState;
    if (hostState === null || !onStateChange) return;
    const wanted = yamlKind === 'file' ? 'docked' : 'expanded';
    if (hostState === wanted) return;
    if (hostActive && !opensStep) onStateChange(wanted);
    else setYamlKind(hostState === 'docked' ? 'file' : 'step');
  }, [hostState, onStateChange, hostActive, yamlKind]);

  return { yamlKind, setYamlKind, sectionOpen, setSectionOpen };
}
