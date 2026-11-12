// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactNode } from 'react';
import { Status } from '@/api/v1/schema';
import StatusChip, { type StatusChipProps } from '@/components/ui/status-chip';
import { useListRunDisplay } from '../../hooks/useListRunDisplay';

interface RunStatusChipProps {
  /** The workflow name of the run. */
  name: string;
  dagRunId?: string;
  status?: Status;
  size?: StatusChipProps['size'];
  /** The status label of the API, as the list has it. */
  children: ReactNode;
}

/**
 * The status chip of a run in a list. A running run that never ends and has
 * no failed step reads "Healthy" (see runDisplayStatus); every other run
 * reads as its API status says.
 */
export function RunStatusChip({
  name,
  dagRunId,
  status,
  size,
  children,
}: RunStatusChipProps) {
  const display = useListRunDisplay(name, dagRunId, status);
  return (
    <StatusChip status={status} healthy={display?.healthy} size={size}>
      {children}
    </StatusChip>
  );
}
