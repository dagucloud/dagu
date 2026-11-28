// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { PlaintextReason } from '@/api/v1/schema';
import H2CCallers from '../H2CCallers';
import ServiceCard from '../ServiceCard';

describe('ServiceCard', () => {
  // System status lists the processes other than workers that still call a
  // coordinator over h2c, under that coordinator (Spec 108 §Workers Page).
  it('lists the h2c callers under their coordinator', () => {
    render(
      <ServiceCard
        title="Coordinator Service"
        icon={null}
        instances={[
          {
            instanceId: 'coordinator-1',
            host: 'coord.example.com',
            port: 50055,
            status: 'active',
            startedAt: new Date().toISOString(),
            detail: (
              <H2CCallers
                callers={[
                  {
                    host: '10.0.0.5',
                    reason: PlaintextReason.no_TLS_source,
                    lastCallAt: new Date().toISOString(),
                  },
                ]}
              />
            ),
          },
        ]}
      />
    );

    expect(screen.getByText('coord.example.com:50055')).toBeInTheDocument();
    expect(
      screen.getByText('Plaintext calls in the last 10 minutes')
    ).toBeInTheDocument();
    expect(screen.getByText('10.0.0.5')).toBeInTheDocument();
    expect(screen.getByText(/no TLS source/)).toBeInTheDocument();
    expect(
      screen.getByText('Set peer.ca_cert_file to use TLS')
    ).toBeInTheDocument();
  });

  it('shows no h2c callers for a coordinator without any', () => {
    render(
      <ServiceCard
        title="Coordinator Service"
        icon={null}
        instances={[
          {
            instanceId: 'coordinator-1',
            host: 'coord.example.com',
            port: 50055,
            status: 'active',
            startedAt: new Date().toISOString(),
          },
        ]}
      />
    );

    expect(screen.queryByText(/Plaintext calls/)).not.toBeInTheDocument();
  });
});
