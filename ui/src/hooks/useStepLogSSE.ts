import { SSEState, useSSE } from './useSSE';

export interface StepLogSSEResponse {
  stdoutContent: string;
  stderrContent: string;
  lineCount: number;
  totalLines: number;
  hasMore: boolean;
}

export function useStepLogSSE(
  name: string,
  dagRunId: string,
  stepName: string,
  enabled: boolean = true,
  remoteNode?: string
): SSEState<StepLogSSEResponse> {
  const endpoint = `/events/dag-runs/${encodeURIComponent(name)}/${encodeURIComponent(dagRunId)}/logs/steps/${encodeURIComponent(stepName)}`;
  return useSSE<StepLogSSEResponse>(endpoint, enabled, remoteNode);
}
