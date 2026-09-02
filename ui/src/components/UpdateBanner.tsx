import { useConfig } from '@/contexts/ConfigContext';
import { X } from 'lucide-react';
import * as React from 'react';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';

export function UpdateBanner() {
  const config = useConfig();
  const [dismissed, setDismissed] = React.useState(() => {
    return (
      localStorage.getItem('update-banner-dismissed') === config.latestVersion
    );
  });

  if (!config.updateAvailable || dismissed) return null;

  const handleDismiss = () => {
    localStorage.setItem('update-banner-dismissed', config.latestVersion);
    setDismissed(true);
  };

  return (
    <div className="bg-violet-50 dark:bg-[#1c1840] border-b border-violet-200 dark:border-[#3a3170] px-4 py-1.5 flex items-center justify-between text-sm">
      <span className="text-violet-900 dark:text-violet-200">
        <I18nTemplate
          text="Update available: v{current} → {latest} {releaseLink} · Run {command} to update"
          values={{
            current: config.version,
            latest: config.latestVersion,
            releaseLink: (
              <a
                href="https://github.com/dagucloud/dagu/releases"
                target="_blank"
                rel="noopener noreferrer"
                className="ml-2 underline hover:no-underline"
              >
                <I18nText text="View release" />
              </a>
            ),
            command: (
              <code className="font-mono bg-violet-100 dark:bg-[#2a2452] px-1 rounded text-xs">
                dagu upgrade
              </code>
            ),
          }}
        />
      </span>
      <I18nProps>
        <button
          onClick={handleDismiss}
          className="p-0.5 hover:bg-violet-100 dark:hover:bg-[#2a2452] rounded"
          aria-label="Dismiss update notification"
        >
          <X className="h-4 w-4" />
        </button>
      </I18nProps>
    </div>
  );
}
