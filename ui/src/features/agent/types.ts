// Message types
export type MessageType =
  | 'user'
  | 'assistant'
  | 'error'
  | 'ui_action'
  | 'user_prompt';

export type UIActionType = 'navigate';

export interface UIAction {
  type: UIActionType;
  path?: string;
}

export interface ToolCall {
  id: string;
  type: string;
  function: {
    name: string;
    arguments: string;
  };
}

export interface ToolResult {
  tool_call_id: string;
  content: string;
  is_error?: boolean;
}

export interface UserPromptOption {
  id: string;
  label: string;
  description?: string;
}

export type PromptType = 'general' | 'command_approval';

export interface UserPrompt {
  prompt_id: string;
  question: string;
  options?: UserPromptOption[];
  allow_free_text: boolean;
  free_text_placeholder?: string;
  multi_select: boolean;
  prompt_type?: PromptType;
  command?: string;
  working_dir?: string;
}

export interface UserPromptResponse {
  prompt_id: string;
  selected_option_ids?: string[];
  free_text_response?: string;
  cancelled?: boolean;
}

export interface TokenUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

export interface Message {
  id: string;
  session_id: string;
  type: MessageType;
  sequence_id: number;
  content?: string;
  tool_calls?: ToolCall[];
  tool_results?: ToolResult[];
  ui_action?: UIAction;
  user_prompt?: UserPrompt;
  usage?: TokenUsage;
  cost?: number;
  delegate_ids?: string[];
  created_at: string;
}

// Session types
export interface Session {
  id: string;
  user_id?: string;
  title?: string;
  created_at: string;
  updated_at: string;
  parent_session_id?: string;
  delegate_task?: string;
}

export interface SessionState {
  session_id: string;
  working: boolean;
  has_pending_prompt?: boolean;
  model?: string;
  total_cost?: number;
}

export interface SessionWithState {
  session: Session;
  working: boolean;
  has_pending_prompt?: boolean;
  model?: string;
  total_cost?: number;
}

// DAG context types
export interface DAGContext {
  dag_file: string;
  dag_run_id?: string;
}

// Delegate status type shared across event and snapshot types
export type DelegateStatus = 'running' | 'completed';

// Delegate event type values sent by the backend ("started" | "completed")
export type DelegateEventType = 'started' | 'completed';

// Delegate event types
export interface DelegateEvent {
  type: DelegateEventType;
  delegate_id: string;
  task: string;
  cost?: number;
}

export interface DelegateInfo {
  id: string;
  task: string;
  status: DelegateStatus;
  zIndex: number;
  positionIndex: number;
}

export interface DelegateMessages {
  delegate_id: string;
  messages: Message[];
}

// DelegateSnapshot is returned by the backend for state recovery on reconnect/reload.
export interface DelegateSnapshot {
  id: string;
  task: string;
  status: DelegateStatus;
  cost?: number;
}

export interface StreamResponse {
  messages?: Message[];
  session?: Session;
  session_state?: SessionState;
  delegate_event?: DelegateEvent;
  delegate_messages?: DelegateMessages;
  delegates?: DelegateSnapshot[];
}

// Tool input types for specialized viewers
export interface BashToolInput {
  command: string;
  timeout?: number;
}

export interface ReadToolInput {
  path: string;
  offset?: number;
  limit?: number;
}

export interface PatchToolInput {
  path: string;
  operation?: 'create' | 'replace' | 'append' | 'insert_before' | 'insert_after' | 'delete';
  content?: string;
  old_string?: string;
  new_string?: string;
  anchor?: string;
}

export interface ThinkToolInput {
  thought: string;
}

export interface NavigateToolInput {
  path: string;
}

export interface AskUserToolInput {
  question: string;
  options?: string[];
  allow_free_text?: boolean;
  free_text_placeholder?: string;
  multi_select?: boolean;
}
