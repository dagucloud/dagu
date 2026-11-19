// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { BackfillStatus, Status } from '@/api/v1/schema';
import { StatusChip, type StatusChipProps } from '@/components/ui/status-chip';

/** The run status whose color a backfill status takes, and its label. */
const LOOK: Record<BackfillStatus, { status: Status; label: string }> = {
  [BackfillStatus.running]: { status: Status.Running, label: 'Running' },
  [BackfillStatus.waiting_for_scheduler]: {
    status: Status.Waiting,
    label: 'Waiting',
  },
  [BackfillStatus.succeeded]: { status: Status.Success, label: 'Succeeded' },
  [BackfillStatus.failed]: { status: Status.Failed, label: 'Failed' },
  [BackfillStatus.cancelled]: { status: Status.Aborted, label: 'Cancelled' },
};

/** A backfill's status, in the colors of run statuses. */
export function BackfillStatusChip({
  status,
  size = 'sm',
}: {
  status: BackfillStatus;
  size?: StatusChipProps['size'];
}) {
  const look = LOOK[status];
  return (
    <StatusChip status={look.status} size={size}>
      {look.label}
    </StatusChip>
  );
}
