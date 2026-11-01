import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Status } from '@/api/v1/schema';
import StatusIcon from '../status-icon';

describe('StatusIcon', () => {
  it.each([
    [Status.NotStarted, 'Not started'],
    [Status.Running, 'Running'],
    [Status.Failed, 'Failed'],
    [Status.Aborted, 'Aborted'],
    [Status.Success, 'Succeeded'],
    [Status.Queued, 'Queued'],
    [Status.PartialSuccess, 'Partially succeeded'],
    [Status.Waiting, 'Waiting'],
    [Status.Rejected, 'Rejected'],
  ])('names status %i with its text', (status, text) => {
    render(<StatusIcon status={status} />);

    const icon = screen.getByRole('img', { name: text });
    expect(icon).toHaveAttribute('title', text);
    expect(icon).not.toHaveTextContent(/\S/);
  });

  it('spins only while the run is running', () => {
    const { container, rerender } = render(
      <StatusIcon status={Status.Running} />
    );
    expect(container.querySelector('svg')).toHaveClass('animate-spin');

    rerender(<StatusIcon status={Status.Success} />);
    expect(container.querySelector('svg')).not.toHaveClass('animate-spin');
  });

  it('renders nothing without a status', () => {
    const { container } = render(<StatusIcon />);
    expect(container).toBeEmptyDOMElement();
  });

  it('is a calm heart named Healthy for a run that never ends', () => {
    const { container } = render(
      <StatusIcon status={Status.Running} healthy />
    );
    const icon = screen.getByRole('img', { name: 'Healthy' });
    expect(icon).toHaveAttribute('title', 'Healthy');
    expect(icon).toHaveAttribute('data-status', String(Status.Running));
    expect(icon.style.color).toBe('var(--status-success)');
    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('motion-safe:animate-pulse');
    expect(svg).not.toHaveClass('animate-spin');
  });

  it('gives no native title when a tooltip of the parent speaks', () => {
    render(<StatusIcon status={Status.Failed} title={false} />);
    expect(screen.getByRole('img', { name: 'Failed' })).not.toHaveAttribute(
      'title'
    );
  });
});
