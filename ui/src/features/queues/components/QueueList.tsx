// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Layers } from 'lucide-react';
import React from 'react';
import type { components } from '../../../api/v1/schema';
import QueueCard from './QueueCard';
import { I18nText } from '@/i18n/I18nText';

interface QueueListProps {
  queues: components['schemas']['Queue'][];
  isLoading?: boolean;
}

function QueueList({ queues, isLoading }: QueueListProps) {
  if (isLoading) {
    return (
      <div className="flex h-32 items-center justify-center">
        <div className="space-y-2 text-center">
          <Layers className="mx-auto h-8 w-8 animate-pulse text-muted-foreground" />
          <p className="text-sm text-muted-foreground"><I18nText text={"Loading queues..."} /></p>
        </div>
      </div>
    );
  }

  if (queues.length === 0) {
    return (
      <div className="flex h-32 items-center justify-center">
        <div className="space-y-2 text-center">
          <Layers className="mx-auto h-8 w-8 text-muted-foreground" />
          <p className="text-sm text-muted-foreground"><I18nText text={"No queues found"} /></p>
        </div>
      </div>
    );
  }

  return (
    <div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">
      {queues.map((queue) => (
        <QueueCard key={queue.name} queue={queue} />
      ))}
    </div>
  );
}

export default QueueList;
