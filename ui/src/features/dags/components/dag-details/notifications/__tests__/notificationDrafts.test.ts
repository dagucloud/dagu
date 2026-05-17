import { describe, expect, it } from 'vitest';

import { NotificationProviderType } from '@/api/v1/schema';
import {
  blankChannel,
  blankTarget,
  channelInput,
  DEFAULT_EMAIL_BODY_TEMPLATE,
  DEFAULT_MESSAGE_TEMPLATE,
  DEFAULT_SUBJECT_TEMPLATE,
  isDefaultDeliveryName,
  replaceDeliveryProvider,
  targetInput,
} from '../notificationDrafts';

describe('notificationDrafts', () => {
  it('uses editable default templates for new channels', () => {
    const channel = blankChannel(NotificationProviderType.slack);
    channel.name = 'Ops Slack';
    channel.slack.webhookUrl = 'https://hooks.slack.com/services/test';

    expect(channelInput(channel)).toMatchObject({
      slack: {
        webhookUrl: 'https://hooks.slack.com/services/test',
        messageTemplate: DEFAULT_MESSAGE_TEMPLATE,
      },
    });
  });

  it('uses editable default subject and body templates for email targets', () => {
    const target = blankTarget(NotificationProviderType.email);
    target.name = 'Ops Email';
    target.email.to = 'ops@example.com';

    expect(targetInput(target)).toMatchObject({
      email: {
        to: ['ops@example.com'],
        subjectTemplate: DEFAULT_SUBJECT_TEMPLATE,
        bodyTemplate: DEFAULT_EMAIL_BODY_TEMPLATE,
      },
    });
  });

  it('detects provider-generated names before replacing them on type change', () => {
    expect(
      isDefaultDeliveryName('Email', NotificationProviderType.email)
    ).toBe(true);
    expect(
      isDefaultDeliveryName('Generic Webhook', NotificationProviderType.webhook)
    ).toBe(true);
    expect(
      isDefaultDeliveryName('Ops Alerts', NotificationProviderType.email)
    ).toBe(false);
  });

  it('renames a generated channel name when the provider changes', () => {
    const channel = blankChannel(NotificationProviderType.email);
    const next = replaceDeliveryProvider(
      channel,
      NotificationProviderType.webhook
    );

    expect(next.name).toBe('Webhook');
    expect(next.type).toBe(NotificationProviderType.webhook);
    expect(next.webhook.messageTemplate).toBe(DEFAULT_MESSAGE_TEMPLATE);
  });

  it('keeps a custom channel name when the provider changes', () => {
    const channel = blankChannel(NotificationProviderType.email);
    channel.name = 'Ops Alerts';
    const next = replaceDeliveryProvider(
      channel,
      NotificationProviderType.slack
    );

    expect(next.name).toBe('Ops Alerts');
    expect(next.type).toBe(NotificationProviderType.slack);
  });
});
