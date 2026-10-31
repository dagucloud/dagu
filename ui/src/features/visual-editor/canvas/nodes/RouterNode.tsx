// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Split } from 'lucide-react';
import { memo } from 'react';
import { useI18n } from '@/i18n/I18nProvider';
import { cn } from '@/lib/utils';
import { useCanvasAffordances } from '../affordances';
import { useCanvasActions } from '../context';
import { routePorts, routerSize } from '../layout';
import { Handle, Position, type NodeProps } from '../engine';
import { statusStrokeClass } from '../statusStyle';
import {
  SOURCE_HANDLE,
  TARGET_HANDLE,
  routeHandleId,
  type StepFlowNode,
} from '../types';
import {
  HANDLE_CLASS,
  IssueBadges,
  NodeToolbar,
  RunDot,
  compactTitle,
  failureTitle,
  handlePosition,
} from './NodeParts';
import { AddStalk } from './StepNode';

function outlineClass(
  node: StepFlowNode['data']['node'],
  selected: boolean | undefined,
  highlighted: boolean | undefined
): string {
  if (selected) {
    return 'stroke-primary';
  }
  if (highlighted) {
    return 'stroke-primary/60';
  }
  // The light theme draws a 1.5 px darker outline, as the cards do.
  const width = '[stroke-width:1.5px] dark:[stroke-width:1px]';
  return node.run
    ? `${statusStrokeClass(node.run.status)} ${width}`
    : `stroke-muted-foreground/60 dark:stroke-muted-foreground/50 ${width}`;
}

/**
 * Router step as a diamond, as in the Graph view, with one output port for
 * each route pattern. The ports sit on the edges of the diamond.
 */
function RouterNodeComponent({
  data,
  selected,
  isConnectable,
}: NodeProps<StepFlowNode>) {
  const { ts } = useI18n();
  const { node } = data;
  const { direction, editable, compact = false } = useCanvasActions();
  const affordances = useCanvasAffordances();
  const connectable = isConnectable && editable;
  const routes = node.routes ?? [];
  const openPorts = new Set(affordances ? (data.openPorts ?? []) : []);
  const size = routerSize(routes.length);
  const ports = routePorts(routes.length, size, direction);
  const lr = direction === 'LR';
  const middle = { x: size / 2, y: size / 2 };
  const input = lr ? { x: 0, y: middle.y } : { x: middle.x, y: 0 };
  const output = lr ? { x: size, y: middle.y } : { x: middle.x, y: size };
  const at = (point: { x: number; y: number }) => ({
    left: point.x,
    top: point.y,
  });
  const stalkSide = lr ? Position.Right : Position.Bottom;

  return (
    <div
      data-testid={`ve-canvas-node-${node.key}`}
      data-shape="diamond"
      title={
        compact
          ? compactTitle({
              label: node.label,
              subtitle: node.routeValue ?? node.subtitle,
              run: node.run,
            })
          : failureTitle(node.run)
      }
      role={compact ? 'group' : undefined}
      aria-label={compact ? node.label : undefined}
      style={{ width: size, height: size }}
      className="group relative text-card-foreground"
    >
      <svg
        aria-hidden="true"
        className="absolute inset-0 h-full w-full overflow-visible"
        viewBox={`0 0 ${size} ${size}`}
        preserveAspectRatio="none"
      >
        <polygon
          points={`${size / 2},0 ${size},${size / 2} ${size / 2},${size} 0,${size / 2}`}
          className={cn(
            'fill-card',
            outlineClass(node, selected, data.highlighted)
          )}
          strokeWidth={selected || data.highlighted ? 2 : 1}
          strokeLinejoin="round"
          vectorEffect="non-scaling-stroke"
          style={{ vectorEffect: 'non-scaling-stroke' }}
        />
      </svg>
      <Handle
        id={TARGET_HANDLE}
        type="target"
        position={handlePosition(direction, 'target')}
        style={at(input)}
        isConnectable={connectable}
        className={HANDLE_CLASS}
      />
      <NodeToolbar
        step={node.key}
        label={node.label}
        selected={selected}
        runnable
      />
      {/* Click-through over the handles; the parts with a tooltip take hover. */}
      <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
        <div className="flex w-28 min-w-0 flex-col items-center">
          <div className="pointer-events-auto flex max-w-full items-center gap-1">
            <Split
              className={cn(
                'shrink-0 text-muted-foreground',
                compact ? 'h-5 w-5' : 'h-3.5 w-3.5'
              )}
              aria-hidden="true"
            />
            {!compact && (
              <span
                className="min-w-0 truncate whitespace-nowrap text-sm font-medium"
                title={node.label}
              >
                {node.label}
              </span>
            )}
            <IssueBadges
              issues={node.issues}
              issueMessages={node.issueMessages}
            />
            <RunDot run={node.run} dotOnly={compact} />
          </div>
          {!compact && (node.routeValue ?? node.subtitle) && (
            <span
              // Two lines: an expression such as ${steps.produce.outputs.code}
              // is read in full, not cut after 12 characters.
              className="pointer-events-auto line-clamp-2 max-w-full break-all text-center font-mono text-xs leading-tight text-muted-foreground"
              title={node.routeValue ?? node.subtitle}
            >
              {node.routeValue ?? node.subtitle}
            </span>
          )}
        </div>
      </div>
      {routes.map((pattern, index) => {
        const port = ports[index] ?? output;
        const stalk = openPorts.has(pattern);
        // The pattern moves out of the way of a stalk: beside its line when
        // the stalk goes down, beyond its button when it goes right.
        const labelPlace = stalk
          ? lr
            ? { left: port.x + 52, top: port.y - 8 }
            : { left: port.x + 6, top: port.y + 8 }
          : lr
            ? { left: port.x + 8, top: port.y - 14 }
            : { left: port.x, top: port.y + 8 };
        return (
          <div key={`${index}:${pattern}`}>
            <Handle
              id={routeHandleId(pattern)}
              type="source"
              position={lr ? Position.Right : Position.Bottom}
              style={at(port)}
              isConnectable={connectable}
              className={HANDLE_CLASS}
            />
            {/* A route with a target has its pattern on the edge. */}
            {(node.routeTargets?.[pattern]?.length ?? 0) === 0 && (
              <span
                data-testid="ve-canvas-route"
                title={pattern}
                style={labelPlace}
                className={cn(
                  'absolute max-w-24 truncate rounded bg-card/80 px-0.5 font-mono text-[11px] leading-4 text-foreground/70',
                  !lr && !stalk && '-translate-x-1/2'
                )}
              >
                {pattern}
              </span>
            )}
            {affordances && stalk && (
              <AddStalk
                side={stalkSide}
                at={at(port)}
                label={ts('Add a step to route {port}', { port: pattern })}
                onAdd={() =>
                  affordances.openPalette({
                    mode: 'route',
                    router: node.key,
                    port: pattern,
                  })
                }
              />
            )}
          </div>
        );
      })}
      <Handle
        id={SOURCE_HANDLE}
        type="source"
        position={handlePosition(direction, 'source')}
        style={at(output)}
        isConnectable={connectable && routes.length === 0}
        // The routes have their own ports: the default one stays for the
        // edges that use it, but it is not drawn.
        className={cn(HANDLE_CLASS, routes.length > 0 && 'invisible')}
      />
      {affordances && data.isSink && routes.length === 0 && (
        <AddStalk
          side={stalkSide}
          at={at(output)}
          label={ts('Add a step')}
          onAdd={() =>
            affordances.openPalette({ mode: 'child', source: node.key })
          }
        />
      )}
    </div>
  );
}

export const RouterNode = memo(RouterNodeComponent);
