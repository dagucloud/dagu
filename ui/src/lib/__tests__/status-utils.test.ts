import { NodeStatus, Status } from '@/api/v1/schema';
import { describe, expect, it } from 'vitest';
import {
  getNodeStatusIcon,
  getStatusClass,
  getStatusColors,
  isActiveNodeStatus,
  isLiveNodeStatus,
  isLiveRunStatus,
  nodeStatusText,
  runStatusText,
  statusLabelText,
} from '../status-utils';

describe('status-utils', () => {
  it('treats retrying as an active warning status', () => {
    expect(isActiveNodeStatus(NodeStatus.Retrying)).toBe(true);
    expect(getStatusClass(NodeStatus.Retrying)).toBe('status-warning');
    expect(getStatusColors(NodeStatus.Retrying)).toMatchObject({
      bgClass: 'bg-[var(--status-warning)]',
      animation: 'animate-pulse',
    });
  });

  // The time of a run or a step that waits for a person still counts.
  it('counts the time of a running or a waiting run and step', () => {
    expect(isLiveRunStatus(Status.Running)).toBe(true);
    expect(isLiveRunStatus(Status.Waiting)).toBe(true);
    expect(isLiveRunStatus(Status.Success)).toBe(false);
    expect(isLiveNodeStatus(NodeStatus.Waiting)).toBe(true);
    expect(isLiveNodeStatus(NodeStatus.Retrying)).toBe(true);
    expect(isLiveNodeStatus(NodeStatus.Success)).toBe(false);
  });

  it('exposes a distinct retrying icon', () => {
    expect(getNodeStatusIcon(NodeStatus.Retrying)).toBe('↻');
  });

  it('turns API status labels into readable text', () => {
    expect(statusLabelText('not_started')).toBe('Not started');
    expect(statusLabelText('partially_succeeded')).toBe('Partially succeeded');
    expect(statusLabelText('retrying')).toBe('Retrying');
    expect(statusLabelText('custom label')).toBe('custom label');
    expect(statusLabelText('__proto__')).toBe('__proto__');
    expect(statusLabelText('constructor')).toBe('constructor');
  });

  it('names run and step statuses the same way', () => {
    expect(runStatusText(Status.Success)).toBe('Succeeded');
    expect(runStatusText(Status.Queued)).toBe('Queued');
    expect(nodeStatusText(NodeStatus.Skipped)).toBe('Skipped');
    expect(nodeStatusText(NodeStatus.PartialSuccess)).toBe(
      'Partially succeeded'
    );
  });
});
