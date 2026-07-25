// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { FileCode2, Gauge } from 'lucide-react';
import { Link } from 'react-router-dom';

import { Badge } from '@/components/ui/badge';
import { Tab, Tabs } from '@/components/ui/tabs';
import Title from '@/components/ui/title';
import type { ControllerDetail } from '../types';
import { ControllerStatusChip } from './ControllerStatusChip';

export function ControllerPageHeader({
  detail,
  activeTab,
  dirty = false,
  actions,
}: {
  detail: ControllerDetail;
  activeTab: 'status' | 'spec';
  dirty?: boolean;
  actions?: React.ReactNode;
}) {
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <Title>{detail.definition.name}</Title>
            <Badge variant="warning">Experimental</Badge>
            {dirty && <Badge variant="warning">Unsaved</Badge>}
            <ControllerStatusChip
              status={detail.runtime.status}
              finishedAt={detail.runtime.finishedAt}
            />
          </div>
          <button
            type="button"
            title="Copy Controller ID"
            className="mt-1 font-mono text-xs text-muted-foreground hover:text-foreground"
            onClick={() => void navigator.clipboard.writeText(detail.id)}
          >
            {detail.id}
          </button>
        </div>
        <div className="flex flex-wrap items-center gap-2">{actions}</div>
      </div>
      <Tabs className="w-full">
        <Tab isActive={activeTab === 'status'} asChild>
          <Link to={`/controllers/${encodeURIComponent(detail.id)}/status`}>
            <Gauge className="h-4 w-4" />
            Status
          </Link>
        </Tab>
        <Tab isActive={activeTab === 'spec'} asChild>
          <Link to={`/controllers/${encodeURIComponent(detail.id)}/spec`}>
            <FileCode2 className="h-4 w-4" />
            Definition
          </Link>
        </Tab>
      </Tabs>
    </div>
  );
}
