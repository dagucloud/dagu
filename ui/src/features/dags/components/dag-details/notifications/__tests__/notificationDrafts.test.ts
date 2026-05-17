import { describe, expect, it } from 'vitest';

import { NotificationProviderType } from '@/api/v1/schema';
import {
  blankChannel,
  blankTarget,
  channelInput,
  targetInput,
} from '../notificationDrafts';

describe('notificationDrafts', () => {
  it('includes message templates in channel input', () => {
    const channel = blankChannel(NotificationProviderType.slack);
    channel.name = 'Ops Slack';
    channel.slack.webhookUrl = 'https://hooks.slack.com/services/test';
    channel.slack.messageTemplate =
      'DAG {{dag.name}} {{run.status}}: {{run.error}}';

    expect(channelInput(channel)).toMatchObject({
      slack: {
        webhookUrl: 'https://hooks.slack.com/services/test',
        messageTemplate: 'DAG {{dag.name}} {{run.status}}: {{run.error}}',
      },
    });
  });

  it('includes email subject and body templates in DAG target input', () => {
    const target = blankTarget(NotificationProviderType.email);
    target.name = 'Ops Email';
    target.email.to = 'ops@example.com';
    target.email.subjectTemplate = '{{dag.name}} {{run.status}}';
    target.email.bodyTemplate = 'Run {{run.id}} failed: {{run.error}}';

    expect(targetInput(target)).toMatchObject({
      email: {
        to: ['ops@example.com'],
        subjectTemplate: '{{dag.name}} {{run.status}}',
        bodyTemplate: 'Run {{run.id}} failed: {{run.error}}',
      },
    });
  });
});
