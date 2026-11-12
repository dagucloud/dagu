// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Download } from 'lucide-react';
import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { useResolvedTheme } from '@/contexts/UserPreference';
import { useI18n } from '@/i18n/I18nProvider';
import type { CanvasDirection, CanvasGraph } from '../contracts';
import { buildExportSvg, downloadPng, downloadSvg } from './exportCanvas';
import type { NodePositions } from './types';

/**
 * Menu that saves the graph as an SVG or a PNG file. The positions are the
 * ones on screen, so a node that the user moved is saved where it is.
 */
export function ExportMenu({
  graph,
  positions,
  direction,
  name,
  compact = false,
  waypoints,
}: {
  graph: CanvasGraph;
  positions: NodePositions;
  direction: CanvasDirection;
  /** File name without extension. */
  name: string;
  /** True when the cards show only their icon. */
  compact?: boolean;
  /** The points that the long edges cross, by edge id, from the layout. */
  waypoints?: Record<string, { x: number; y: number }[]>;
}): ReactElement {
  const { ts } = useI18n();
  const dark = useResolvedTheme() === 'dark';
  const build = () =>
    buildExportSvg(graph, positions, direction, { dark, compact, waypoints });

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          className="h-7 px-2 text-xs"
          aria-label={ts('Export graph')}
          title={ts('Export graph')}
        >
          <Download className="h-3.5 w-3.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuItem onSelect={() => downloadSvg(build().svg, name)}>
          {ts('Export as SVG')}
        </DropdownMenuItem>
        <DropdownMenuItem
          onSelect={() => {
            const { svg, width, height } = build();
            void downloadPng(svg, width, height, name).catch(() => {});
          }}
        >
          {ts('Export as PNG')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
