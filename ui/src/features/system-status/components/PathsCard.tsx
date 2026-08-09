import {
  Check,
  Copy,
  Database,
  FileText,
  FolderOpen,
  GitBranch,
  ScrollText,
  Settings,
} from 'lucide-react';
import React from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useConfig, type PathsConfig } from '../../../contexts/ConfigContext';
import { cn } from '../../../lib/utils';

interface PathRowProps {
  label: string;
  path: string;
}

function PathRow({ label, path }: PathRowProps) {
  const [copied, setCopied] = React.useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(path);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard API might not be available
    }
  };

  return (
    <button
      type="button"
      onClick={handleCopy}
      aria-label={`Copy ${label} path`}
      className={cn(
        'w-full flex items-center justify-between gap-3 px-3 py-2 rounded-lg cursor-pointer transition-all text-left',
        'hover:bg-accent/50 group focus-visible:bg-accent/50'
      )}
    >
      <span className="text-xs text-muted-foreground shrink-0 w-20">
        {label}
      </span>
      <div className="flex-1 min-w-0 overflow-hidden">
        <code className="font-mono text-xs text-foreground block overflow-x-auto whitespace-nowrap no-scrollbar">
          {path || '-'}
        </code>
      </div>
      <div
        className={cn(
          'shrink-0 transition-all',
          copied
            ? 'text-success'
            : 'text-muted-foreground/50 group-hover:text-muted-foreground'
        )}
      >
        {copied ? (
          <Check className="h-3.5 w-3.5" />
        ) : (
          <Copy className="h-3.5 w-3.5" />
        )}
      </div>
    </button>
  );
}

interface PathGroupProps {
  icon: React.ReactNode;
  title: string;
  children: React.ReactNode;
}

function PathGroup({ icon, title, children }: PathGroupProps) {
  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2 px-3 py-1.5">
        <span className="text-muted-foreground">{icon}</span>
        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          {title}
        </span>
      </div>
      <div className="space-y-0.5">{children}</div>
    </div>
  );
}

function PathsCard() {
  const [open, setOpen] = React.useState(false);
  const config = useConfig();
  const paths: PathsConfig = config.paths;

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <FolderOpen className="h-4 w-4" />
        Paths
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-xl p-0 overflow-hidden">
          <DialogHeader className="px-4 pt-4 pb-3 border-b border-border">
            <DialogTitle className="flex items-center gap-2 text-base">
              <FolderOpen className="h-4 w-4 text-primary" />
              System Paths
            </DialogTitle>
            <p className="text-xs text-muted-foreground mt-1">
              Click any path to copy to clipboard
            </p>
          </DialogHeader>
          {paths ? (
            <div className="px-3 py-4 space-y-4 max-h-[60vh] overflow-y-auto no-scrollbar">
              <PathGroup
                icon={<Settings className="h-3.5 w-3.5" />}
                title="Configuration"
              >
                <PathRow label="Config" path={paths.configFileUsed} />
                <PathRow label="Base" path={paths.baseConfig} />
              </PathGroup>

              <PathGroup
                icon={<Database className="h-3.5 w-3.5" />}
                title="Data"
              >
                <PathRow label="DAGs" path={paths.dagsDir} />
                <PathRow
                  label="Wiki"
                  path={paths.wikiDir ?? paths.docsDir ?? ''}
                />
                <PathRow label="DAG Runs" path={paths.dagRunsDir} />
                <PathRow label="Queue" path={paths.queueDir} />
                <PathRow label="Process" path={paths.procDir} />
                <PathRow label="Services" path={paths.serviceRegistryDir} />
                <PathRow label="Suspend" path={paths.suspendFlagsDir} />
              </PathGroup>

              <PathGroup
                icon={<ScrollText className="h-3.5 w-3.5" />}
                title="Logs"
              >
                <PathRow label="Logs" path={paths.logDir} />
                <PathRow label="Admin" path={paths.adminLogsDir} />
                <PathRow label="Audit" path={paths.auditLogsDir} />
              </PathGroup>

              {config.gitSyncEnabled && (
                <PathGroup
                  icon={<GitBranch className="h-3.5 w-3.5" />}
                  title="Git Sync"
                >
                  <PathRow label="Sync Dir" path={paths.gitSyncDir} />
                </PathGroup>
              )}
            </div>
          ) : (
            <div className="px-6 py-8 text-center">
              <FileText className="h-8 w-8 text-muted-foreground/50 mx-auto mb-2" />
              <p className="text-sm text-muted-foreground">
                No path information available
              </p>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

export default PathsCard;
