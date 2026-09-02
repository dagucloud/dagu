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
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

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
        <I18nText text={'Paths'} />
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-xl p-0 overflow-hidden">
          <DialogHeader className="border-b border-border px-3 py-2">
            <DialogTitle className="flex items-center gap-2 text-base">
              <FolderOpen className="h-4 w-4 text-primary" />
              <I18nText text={'System Paths'} />
            </DialogTitle>
            <p className="text-xs text-muted-foreground mt-1">
              <I18nText text={'Click any path to copy to clipboard'} />
            </p>
          </DialogHeader>
          {paths ? (
            <div className="px-3 py-4 space-y-4 max-h-[60vh] overflow-y-auto no-scrollbar">
              <I18nProps>
                <PathGroup
                  icon={<Settings className="h-3.5 w-3.5" />}
                  title="Configuration"
                >
                  <I18nProps>
                    <PathRow label="Config" path={paths.configFileUsed} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Base" path={paths.baseConfig} />
                  </I18nProps>
                </PathGroup>
              </I18nProps>

              <I18nProps>
                <PathGroup
                  icon={<Database className="h-3.5 w-3.5" />}
                  title="Data"
                >
                  <I18nProps>
                    <PathRow label="DAGs" path={paths.dagsDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow
                      label="Wiki"
                      path={paths.wikiDir ?? paths.docsDir ?? ''}
                    />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="DAG Runs" path={paths.dagRunsDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Queue" path={paths.queueDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Process" path={paths.procDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Services" path={paths.serviceRegistryDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Suspend" path={paths.suspendFlagsDir} />
                  </I18nProps>
                </PathGroup>
              </I18nProps>

              <I18nProps>
                <PathGroup
                  icon={<ScrollText className="h-3.5 w-3.5" />}
                  title="Logs"
                >
                  <I18nProps>
                    <PathRow label="Logs" path={paths.logDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Admin" path={paths.adminLogsDir} />
                  </I18nProps>
                  <I18nProps>
                    <PathRow label="Audit" path={paths.auditLogsDir} />
                  </I18nProps>
                </PathGroup>
              </I18nProps>

              {config.gitSyncEnabled && (
                <I18nProps>
                  <PathGroup
                    icon={<GitBranch className="h-3.5 w-3.5" />}
                    title="Git Sync"
                  >
                    <I18nProps>
                      <PathRow label="Sync Dir" path={paths.gitSyncDir} />
                    </I18nProps>
                  </PathGroup>
                </I18nProps>
              )}
            </div>
          ) : (
            <div className="px-6 py-8 text-center">
              <FileText className="h-8 w-8 text-muted-foreground/50 mx-auto mb-2" />
              <p className="text-sm text-muted-foreground">
                <I18nText text={'No path information available'} />
              </p>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

export default PathsCard;
