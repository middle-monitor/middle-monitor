import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Host, Service } from '../api';
import ServiceModal from './ServiceModal';

const update = vi.fn();
let editingService: Partial<Service> | null = null;

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock('@tanstack/react-query', () => ({ useQueryClient: () => ({}) }));
vi.mock('../api', () => ({}));
vi.mock('../queryClient', () => ({ invalidateServiceWrite: vi.fn() }));
vi.mock('../hooks/useOrgApi', () => ({
  useOrgApi: () => ({ services: { update, create: vi.fn() }, hosts: { create: vi.fn() } }),
  useOrgQueryScope: () => 'test',
}));
vi.mock('../contexts/ServiceModalContext', () => ({
  useServiceModal: () => ({
    isOpen: true,
    editingService,
    hosts: [{ id: 1, name: 'box', service: 'box' } as Host],
    closeModal: vi.fn(),
  }),
}));

// vite.config.ts sets no setup file, so @testing-library/react never installs its own cleanup.
afterEach(cleanup);
beforeEach(() => update.mockReset().mockResolvedValue({}));

function submit(service: Partial<Service>) {
  editingService = { id: 80, host_id: 1, name: 'job', max_attempts: 3, ...service };
  render(<ServiceModal />);
  fireEvent.submit(document.querySelector('form')!);
}

// A heartbeat's interval is the job's period (the API accepts up to 31 days),
// while every other check is polled by the scheduler and stays capped at an hour.
describe('ServiceModal interval on save', () => {
  it('saves a weekly heartbeat', async () => {
    submit({ type: 'heartbeat', service_interval: 7 * 24 * 3600, credentials: '{"grace_seconds":7200}' });
    await waitFor(() => expect(update).toHaveBeenCalled());
    expect(update.mock.calls[0][1]).toMatchObject({ service_interval: 7 * 24 * 3600 });
  });

  it('still rejects a polled check slower than an hour', async () => {
    submit({ type: 'http', host: 'https://example.com', service_interval: 7200 });
    expect(await screen.findByText('service_modal.errors.interval_out_of_range')).toBeTruthy();
    expect(update).not.toHaveBeenCalled();
  });
});
