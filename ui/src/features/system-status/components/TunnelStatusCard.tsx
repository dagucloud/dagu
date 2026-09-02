import { Clock, Copy, ExternalLink, Globe, Lock } from 'lucide-react';
import React from 'react';
import type { components } from '../../../api/v1/schema';
import { Button } from '@/components/ui/button';
import { cn } from '../../../lib/utils';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type TunnelStatusResponse = components['schemas']['TunnelStatusResponse'];

interface TunnelStatusCardProps {
  data: TunnelStatusResponse | undefined;
  isLoading: boolean;
  error?: string;
}

function TunnelStatusCard({ data, isLoading, error }: TunnelStatusCardProps) {
  const [copied, setCopied] = React.useState(false);

  const getStatusColor = (status: string | undefined) => {
    switch (status) {
      case 'connected':
        return 'bg-success';
      case 'connecting':
      case 'reconnecting':
        return 'bg-warning';
      case 'error':
        return 'bg-error';
      default:
        return 'bg-muted-foreground';
    }
  };

  const getStatusLabel = (status: string | undefined) => {
    switch (status) {
      case 'connected':
        return 'Connected';
      case 'connecting':
        return 'Connecting...';
      case 'reconnecting':
        return 'Reconnecting...';
      case 'error':
        return 'Error';
      case 'disabled':
        return 'Disabled';
      default:
        return 'Unknown';
    }
  };

  const getUptime = (startedAt: string | undefined): string => {
    if (!startedAt) return '';
    const start = new Date(startedAt);
    const now = new Date();
    const diff = now.getTime() - start.getTime();

    const days = Math.floor(diff / (1000 * 60 * 60 * 24));
    const hours = Math.floor((diff % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60));
    const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));

    if (days > 0) return `${days}d ${hours}h`;
    if (hours > 0) return `${hours}h ${minutes}m`;
    return `${minutes}m`;
  };

  const handleCopyUrl = async () => {
    if (data?.publicUrl) {
      await navigator.clipboard.writeText(data.publicUrl);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const handleOpenUrl = () => {
    if (data?.publicUrl) {
      window.open(data.publicUrl, '_blank', 'noopener,noreferrer');
    }
  };

  const providerLabels: Record<string, string> = {
    tailscale: 'Tailscale',
  };
  const providerLabel = data?.provider
    ? providerLabels[data.provider] || data.provider
    : '';

  return (
    <div className="border rounded-lg bg-card">
      {/* Header */}
      <div className="flex items-center gap-2 px-3 py-2 border-b">
        <div className="text-muted-foreground">
          {data?.isPublic ? (
            <Globe className="h-4 w-4" />
          ) : (
            <Lock className="h-4 w-4" />
          )}
        </div>
        <h3 className="text-sm font-medium"><I18nText text={"Tunnel Service"} /></h3>
        <span className="text-xs text-muted-foreground ml-auto">
          {data?.isPublic ? <I18nText text={"Public"} /> : <I18nText text={"Private"} />}
        </span>
      </div>

      {/* Content */}
      <div className="px-3 py-2">
        {isLoading && !data && (
          <div className="text-xs text-muted-foreground"><I18nText text={"Loading..."} /></div>
        )}

        {error && <div className="text-xs text-error">{error}</div>}

        {!error && data && (
          <div className="space-y-2">
            {/* Status Row */}
            <div className="flex items-center gap-3 text-xs">
              {/* Status indicator */}
              <div className="relative flex-shrink-0">
                <div
                  className={cn(
                    'w-1.5 h-1.5 rounded-full',
                    getStatusColor(data.status)
                  )}
                />
                {data.status === 'connected' && (
                  <div
                    className={cn(
                      'absolute inset-0 rounded-full animate-ping opacity-75',
                      getStatusColor(data.status)
                    )}
                  />
                )}
              </div>
              <span className="text-muted-foreground">
                {getStatusLabel(data.status)}
              </span>

              {/* Provider & Mode */}
              {providerLabel && (
                <span className="font-mono text-muted-foreground">
                  {providerLabel}
                  {data?.mode && ` (${data.mode})`}
                </span>
              )}

              {/* Uptime */}
              {data.status === 'connected' && data.startedAt && (
                <span className="flex items-center gap-1 text-muted-foreground ml-auto">
                  <Clock className="h-3 w-3" />
                  {getUptime(data.startedAt)}
                </span>
              )}
            </div>

            {/* URL Row */}
            {data.publicUrl && (
              <div className="flex items-center gap-2">
                <code className="text-xs font-mono text-foreground bg-muted px-1.5 py-0.5 rounded flex-1 truncate">
                  {data.publicUrl}
                </code>
                <I18nProps><Button
                  size="icon"
                  variant="ghost"
                  className="h-6 w-6"
                  onClick={handleCopyUrl}
                  title="Copy URL"
                >
                  <Copy className="h-3 w-3" />
                </Button></I18nProps>
                <I18nProps><Button
                  size="icon"
                  variant="ghost"
                  className="h-6 w-6"
                  onClick={handleOpenUrl}
                  title="Open in new tab"
                >
                  <ExternalLink className="h-3 w-3" />
                </Button></I18nProps>
                {copied && (
                  <span className="text-xs text-success"><I18nText text={"Copied!"} /></span>
                )}
              </div>
            )}

            {/* Error Message */}
            {data.error && (
              <div className="text-xs text-error bg-error/10 px-2 py-1 rounded">
                {data.error}
              </div>
            )}

            {/* Disabled State */}
            {data.status === 'disabled' && !data.enabled && (
              <div className="text-xs text-muted-foreground">
                <I18nText text={"Tunnel is not enabled. Start the server with --tunnel flag to enable."} />
              </div>
            )}
          </div>
        )}

        {!error && !data && !isLoading && (
          <div className="text-xs text-muted-foreground">
            <I18nText text={"Tunnel status unavailable"} />
          </div>
        )}
      </div>
    </div>
  );
}

export default TunnelStatusCard;
