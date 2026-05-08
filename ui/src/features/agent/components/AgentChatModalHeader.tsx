import type { ReactElement } from 'react';
import { useNavigate } from 'react-router-dom';

import {
  PanelLeft,
  Plus,
  Settings,
  Shield,
  ShieldOff,
  X,
} from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { cn } from '@/lib/utils';
import { useUserPreferences } from '@/contexts/UserPreference';

import { useResizableDraggable } from '../hooks/useResizableDraggable';
import { formatCost } from '../utils/formatCost';

type Props = {
  sessionId: string | null;
  totalCost?: number;
  isSidebarOpen: boolean;
  onToggleSidebar: () => void;
  onClearSession: () => void;
  onClose?: () => void;
  dragHandlers?: ReturnType<typeof useResizableDraggable>['dragHandlers'];
  isMobile?: boolean;
};

export function AgentChatModalHeader({
  totalCost,
  isSidebarOpen,
  onToggleSidebar,
  onClearSession,
  onClose,
  dragHandlers,
  isMobile,
}: Props): ReactElement {
  const navigate = useNavigate();
  const { preferences, updatePreference } = useUserPreferences();
  const activeDragHandlers =
    dragHandlers && !isMobile ? dragHandlers : undefined;
  const handleOpenSettings = (): void => {
    navigate('/agent');
  };

  return (
    <div
      className={cn(
        'flex items-center justify-between px-3 py-2 border-b border-border bg-secondary dark:bg-surface',
        activeDragHandlers && 'cursor-move'
      )}
      {...(activeDragHandlers || {})}
    >
      <div className="flex items-center gap-2 flex-1 min-w-0">
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={onToggleSidebar}
          title={isSidebarOpen ? 'Hide sessions' : 'Show sessions'}
          aria-label={isSidebarOpen ? 'Hide sessions' : 'Show sessions'}
          className="flex-shrink-0 text-muted-foreground hover:text-foreground"
        >
          <PanelLeft className="h-4 w-4" />
        </Button>
        <span className="truncate text-sm font-medium text-foreground">
          Agent
        </span>
      </div>
      {totalCost != null && totalCost > 0 && (
        <Badge variant="secondary" className="mr-1 flex-shrink-0 tabular-nums">
          {formatCost(totalCost)}
        </Badge>
      )}
      <TooltipProvider delayDuration={300}>
        <div className="flex items-center gap-1 flex-shrink-0">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() =>
                  updatePreference('safeMode', !preferences.safeMode)
                }
                className="text-muted-foreground hover:text-foreground"
                aria-label={
                  preferences.safeMode
                    ? 'Disable safe mode'
                    : 'Enable safe mode'
                }
                aria-pressed={preferences.safeMode}
              >
                {preferences.safeMode ? (
                  <Shield className="h-4 w-4" />
                ) : (
                  <ShieldOff className="h-4 w-4" />
                )}
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              <p>
                {preferences.safeMode
                  ? 'Safe mode enabled: dangerous commands require approval'
                  : 'Safe mode disabled: all commands execute immediately'}
              </p>
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                onClick={handleOpenSettings}
                className="text-muted-foreground hover:text-foreground"
                aria-label="Open agent settings"
              >
                <Settings className="h-4 w-4" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              <p>Open agent settings</p>
            </TooltipContent>
          </Tooltip>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onClearSession}
            className="text-muted-foreground hover:text-foreground"
            title="New session"
            aria-label="New session"
          >
            <Plus className="h-4 w-4" />
          </Button>
          {onClose && (
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={onClose}
              className="text-muted-foreground hover:text-foreground"
              title="Close"
              aria-label="Close agent"
            >
              <X className="h-4 w-4" />
            </Button>
          )}
        </div>
      </TooltipProvider>
    </div>
  );
}
