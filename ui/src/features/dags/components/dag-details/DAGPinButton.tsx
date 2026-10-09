// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useErrorModal } from '@/components/ui/error-modal';
import { useCanWriteForWorkspace } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import React from 'react';
import { setDAGPinned } from '../../lib/dagPins';
import PinToggle from '../common/PinToggle';

type Props = {
  fileName: string;
  /** Workflow name used in the accessible label. */
  name: string;
  pinned: boolean;
  /** Workspace that decides whether the user may pin. */
  workspace: string;
  /** Called after the pin changes on the server. */
  onChanged?: () => void;
};

/** Pins the workflow for all users from its header. */
function DAGPinButton({ fileName, name, pinned, workspace, onChanged }: Props) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const canPin = useCanWriteForWorkspace(workspace);
  const { showError } = useErrorModal();
  // Reflects the click right away; the parent's refresh confirms it.
  const [shownPinned, setShownPinned] = React.useState(pinned);
  const [pending, setPending] = React.useState(false);

  React.useEffect(() => {
    setShownPinned(pinned);
  }, [pinned]);

  const toggle = async () => {
    const next = !shownPinned;
    setShownPinned(next);
    setPending(true);
    try {
      await setDAGPinned(client, { fileName, pinned: next, remoteNode });
      onChanged?.();
    } catch (error) {
      setShownPinned(!next);
      showError(
        error instanceof Error ? error.message : String(error),
        'Please try again or check the server connection.'
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <PinToggle
      name={name}
      pinned={shownPinned}
      canToggle={canPin}
      pending={pending}
      onToggle={() => void toggle()}
    />
  );
}

export default DAGPinButton;
