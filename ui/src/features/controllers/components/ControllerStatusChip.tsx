// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import StatusChip from '@/components/ui/status-chip';
import { controllerStatusText } from '../types';

export function ControllerStatusChip({
  status,
  finishedAt,
}: {
  status: Status;
  finishedAt?: string;
}) {
  return (
    <StatusChip status={status} size="sm">
      {controllerStatusText(status, finishedAt)}
    </StatusChip>
  );
}
