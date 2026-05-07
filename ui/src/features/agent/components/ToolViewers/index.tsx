import { BashToolViewer } from './BashToolViewer';
import { ReadToolViewer } from './ReadToolViewer';
import { PatchToolViewer } from './PatchToolViewer';
import { ThinkToolViewer } from './ThinkToolViewer';
import { NavigateToolViewer } from './NavigateToolViewer';
import { AskUserToolViewer } from './AskUserToolViewer';
import { DefaultToolViewer } from './DefaultToolViewer';
import type { ToolResult } from '../../types';

export interface ToolViewerProps {
  toolName: string;
  args: Record<string, unknown>;
  toolResult?: ToolResult;
}

const toolViewerRegistry: Record<string, React.FC<ToolViewerProps>> = {
  bash: BashToolViewer,
  read: ReadToolViewer,
  patch: PatchToolViewer,
  think: ThinkToolViewer,
  navigate: NavigateToolViewer,
  ask_user: AskUserToolViewer,
};

export function ToolContentViewer({ toolName, args, toolResult }: ToolViewerProps): React.ReactNode {
  const Viewer = toolViewerRegistry[toolName] || DefaultToolViewer;
  return <Viewer args={args} toolName={toolName} toolResult={toolResult} />;
}

export { BashToolViewer } from './BashToolViewer';
export { ReadToolViewer } from './ReadToolViewer';
export { PatchToolViewer } from './PatchToolViewer';
export { ThinkToolViewer } from './ThinkToolViewer';
export { NavigateToolViewer } from './NavigateToolViewer';
export { AskUserToolViewer } from './AskUserToolViewer';
export { DefaultToolViewer } from './DefaultToolViewer';
