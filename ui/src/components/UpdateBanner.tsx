import { useConfig } from '@/contexts/ConfigContext';
import { X } from 'lucide-react';
import * as React from 'react';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';

const UPDATE_DISMISSED_KEY = 'update-banner-dismissed';
const RESTART_DISMISSED_KEY = 'restart-banner-dismissed';

export function UpdateBanner() {
  const config = useConfig();

  // Restarting is the only step left once a new binary is installed, so it
  // replaces the update notice.
  if (config.restartRequired) {
    return (
      <DismissibleBanner
        key={RESTART_DISMISSED_KEY}
        storageKey={RESTART_DISMISSED_KEY}
        dismissedValue={config.version}
      >
        {config.restartCommand ? (
          <I18nTemplate
            text="A new Dagu binary is installed. Run {command} to restart and use it."
            values={{ command: <Command text={config.restartCommand} /> }}
          />
        ) : (
          <I18nText text="A new Dagu binary is installed. Restart Dagu to use it." />
        )}
      </DismissibleBanner>
    );
  }

  if (!config.updateAvailable) return null;

  const releaseLink = (
    <a
      href="https://github.com/dagucloud/dagu/releases"
      target="_blank"
      rel="noopener noreferrer"
      className="ml-2 underline hover:no-underline"
    >
      <I18nText text="View release" />
    </a>
  );

  return (
    <DismissibleBanner
      key={UPDATE_DISMISSED_KEY}
      storageKey={UPDATE_DISMISSED_KEY}
      dismissedValue={config.latestVersion}
    >
      {config.upgradeCommand ? (
        <I18nTemplate
          text="Update available: v{current} → {latest} {releaseLink} · Run {command} to update"
          values={{
            current: config.version,
            latest: config.latestVersion,
            releaseLink,
            command: <Command text={config.upgradeCommand} />,
          }}
        />
      ) : (
        <I18nTemplate
          text="Update available: v{current} → {latest} {releaseLink}"
          values={{
            current: config.version,
            latest: config.latestVersion,
            releaseLink,
          }}
        />
      )}
    </DismissibleBanner>
  );
}

// DismissibleBanner stays hidden while storageKey holds dismissedValue, so a
// new version brings it back.
function DismissibleBanner({
  storageKey,
  dismissedValue,
  children,
}: {
  storageKey: string;
  dismissedValue: string;
  children: React.ReactNode;
}) {
  const [dismissed, setDismissed] = React.useState(
    () => localStorage.getItem(storageKey) === dismissedValue
  );

  if (dismissed) return null;

  const handleDismiss = () => {
    localStorage.setItem(storageKey, dismissedValue);
    setDismissed(true);
  };

  return (
    <div className="bg-violet-50 dark:bg-[#1c1840] border-b border-violet-200 dark:border-[#3a3170] px-4 py-1.5 flex items-center justify-between text-sm">
      <span className="text-violet-900 dark:text-violet-200">{children}</span>
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

function Command({ text }: { text: string }) {
  return (
    <code className="font-mono bg-violet-100 dark:bg-[#2a2452] px-1 rounded text-xs">
      {text}
    </code>
  );
}
