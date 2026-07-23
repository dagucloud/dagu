// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { ControllerContextMessage } from '../types';

const roleVariant = {
  user: 'primary',
  assistant: 'info',
  tool: 'warning',
} as const;

export function ControllerContext({
  messages,
}: {
  messages: ControllerContextMessage[];
}) {
  if (messages.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
        No Router context has been recorded yet.
      </div>
    );
  }

  return (
    <div className="space-y-3">
      {messages.map((message, index) => (
        <Card key={`${message.role}-${index}`}>
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle className="flex items-center gap-2 capitalize">
              {message.role}
              <Badge variant={roleVariant[message.role]}>{index + 1}</Badge>
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {message.content && (
              <pre className="whitespace-pre-wrap break-words font-sans text-sm leading-relaxed">
                {message.content}
              </pre>
            )}
            {message.tool_calls?.map((call) => (
              <details
                key={call.id}
                className="rounded-md border border-border p-3"
              >
                <summary className="cursor-pointer text-sm font-medium">
                  Tool: {call.function.name}
                </summary>
                <pre className="mt-2 overflow-auto whitespace-pre-wrap break-all text-xs text-muted-foreground">
                  {call.function.arguments}
                </pre>
              </details>
            ))}
            {message.tool_call_id && (
              <p className="text-xs text-muted-foreground">
                Tool call <code>{message.tool_call_id}</code>
              </p>
            )}
            {message.metadata && (
              <details>
                <summary className="cursor-pointer text-xs text-muted-foreground">
                  Message metadata
                </summary>
                <pre className="mt-2 overflow-auto whitespace-pre-wrap break-all text-xs text-muted-foreground">
                  {JSON.stringify(message.metadata, null, 2)}
                </pre>
              </details>
            )}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}
