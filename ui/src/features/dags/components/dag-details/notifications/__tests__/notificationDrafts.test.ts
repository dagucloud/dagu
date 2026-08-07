import { describe, expect, it } from 'vitest';

import { NotificationProviderType } from '@/api/v1/schema';
import {
  blankChannel,
  blankTarget,
  channelInput,
  DEFAULT_EMAIL_BODY_TEMPLATE,
  DEFAULT_MESSAGE_TEMPLATE,
  DEFAULT_SUBJECT_TEMPLATE,
  draftChannelFromAPI,
  isDefaultDeliveryName,
  replaceDeliveryProvider,
  targetInput,
} from '../notificationDrafts';

describe('notificationDrafts', () => {
  it('uses editable default templates for new channels', () => {
    const channel = blankChannel(NotificationProviderType.slack);
    channel.name = 'Ops Slack';
    channel.slack.webhookUrl = 'https://hooks.slack.com/services/test';

    expect(DEFAULT_MESSAGE_TEMPLATE).toContain('{{run.link}}');
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

    expect(DEFAULT_EMAIL_BODY_TEMPLATE).toContain('{{run.link}}');
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

  it('sends an edited webhook body template and omits it when left blank', () => {
    const target = blankTarget(NotificationProviderType.webhook);
    target.webhook.url = 'https://example.com/hook';

    expect(targetInput(target)).toMatchObject({
      webhook: { bodyTemplate: undefined },
    });

    target.webhook.bodyTemplate = '{"text": "{{message}}"}';
    expect(targetInput(target)).toMatchObject({
      webhook: { bodyTemplate: '{"text": "{{message}}"}' },
    });
  });

  it('round-trips a Teams channel between the API and the draft', () => {
    const draft = draftChannelFromAPI({
      id: 'teams-1',
      name: 'Ops Teams',
      type: NotificationProviderType.teams,
      enabled: true,
      createdAt: '2026-08-07T00:00:00Z',
      updatedAt: '2026-08-07T00:00:00Z',
      teams: {
        webhookUrlConfigured: true,
        webhookUrlPreview: 'http...cdef',
        messageTemplate: 'DAG {{dag.name}} {{run.status}}',
      },
    });

    expect(draft.teams.webhookUrlConfigured).toBe(true);
    expect(draft.teams.webhookUrlPreview).toBe('http...cdef');
    expect(draft.teams.messageTemplate).toBe('DAG {{dag.name}} {{run.status}}');

    // A saved URL stays server-side, so an untouched draft must not clear it.
    expect(channelInput(draft)).toMatchObject({
      type: NotificationProviderType.teams,
      teams: {
        webhookUrl: undefined,
        messageTemplate: 'DAG {{dag.name}} {{run.status}}',
      },
    });

    draft.teams.webhookUrl = 'https://example.webhook.office.com/webhookb2/abc';
    expect(channelInput(draft)).toMatchObject({
      teams: { webhookUrl: 'https://example.webhook.office.com/webhookb2/abc' },
    });
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
