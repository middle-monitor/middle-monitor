import { readFileSync, readdirSync } from 'node:fs';
import { test, expect } from '@playwright/test';

import {
  createQueryClient,
  invalidateEntityDelete,
  invalidateServiceWrite,
  orgQueryScope,
} from '../src/queryClient';

// No browser here: what a service write does to the cache is decided in one
// place, and this checks that decision directly. refresh-state.spec.ts drives
// the same write through the UI, behind the demo sandbox's write seam.
// Before this epic, saving the modal dispatched a `serviceUpdated` window
// event and the four views listening to it re-ran their whole loading, losing
// the filter and the page the reader was on.

const scope = orgQueryScope('acme', false);

test('a service write revalidates the service and host lists without changing their keys', async () => {
  const client = createQueryClient();
  const calls: string[] = [];

  // Keys carry the filter and the page: that state must survive the write.
  const servicesList = [scope, 'services', 'list', 'checkout', 'failing', 'http', 2];
  const hostsList = [scope, 'hosts', 'list', '', 'all', 0];

  const seed = async (queryKey: unknown[], label: string) => {
    await client.fetchQuery({
      queryKey,
      queryFn: async () => {
        calls.push(label);
        return label;
      },
      staleTime: Infinity,
    });
  };

  await seed(servicesList, 'services');
  await seed(hostsList, 'hosts');
  expect(calls).toEqual(['services', 'hosts']);

  invalidateServiceWrite(client, scope);

  // Both entries are stale now, so the next read goes back to the network...
  await seed(servicesList, 'services');
  await seed(hostsList, 'hosts');
  expect(calls).toEqual(['services', 'hosts', 'services', 'hosts']);

  // ...and they are still the same entries: same filter, same page.
  const cached = client.getQueryCache().getAll().map((q) => q.queryKey);
  expect(cached).toContainEqual(servicesList);
  expect(cached).toContainEqual(hostsList);
});

test('a service write leaves the views it does not touch alone', async () => {
  const client = createQueryClient();
  let logsCalls = 0;

  const logsSearch = [scope, 'logs', 'search', 'timeout', 'ERROR', '1h', 3];
  await client.fetchQuery({
    queryKey: logsSearch,
    queryFn: async () => { logsCalls += 1; return 'logs'; },
    staleTime: Infinity,
  });

  invalidateServiceWrite(client, scope);

  // A reader paging through logs is not sent back to the network, let alone to
  // the first page, because someone edited a service in another tab of the UI.
  await client.fetchQuery({
    queryKey: logsSearch,
    queryFn: async () => { logsCalls += 1; return 'logs'; },
    staleTime: Infinity,
  });
  expect(logsCalls).toBe(1);
});

test('a delete drops the detail entry instead of leaving it to be repainted', async () => {
  const client = createQueryClient();
  const detail = [scope, 'services', 'detail', 42];

  await client.fetchQuery({
    queryKey: detail,
    queryFn: async () => ({ id: 42, name: 'checkout' }),
    staleTime: Infinity,
  });
  expect(client.getQueryData(detail)).toBeDefined();

  invalidateEntityDelete(client, scope, detail);

  // Nothing left to paint the deleted service from: going back to its URL has
  // to hit the network and land on the not-found screen. Invalidating alone
  // would have kept the data on screen, and the 404 refetch does not replace it
  // — a query that already holds data reports no loading error.
  expect(client.getQueryData(detail)).toBeUndefined();
});

test('a deleted host is dropped whatever date window it was read under', async () => {
  const client = createQueryClient();
  const windows = ['1h', '7d'];

  for (const range of windows) {
    await client.fetchQuery({
      queryKey: [scope, 'hosts', 'detail', '7', range],
      queryFn: async () => ({ id: 7, name: 'web-01' }),
      staleTime: Infinity,
    });
  }

  invalidateEntityDelete(client, scope, [scope, 'hosts', 'detail', '7']);

  // The view holds one entry per date range the reader opened: dropping only
  // the one on screen would leave the host alive under another window.
  for (const range of windows) {
    expect(client.getQueryData([scope, 'hosts', 'detail', '7', range])).toBeUndefined();
  }
});

test('both detail views clear the cache before they navigate away', () => {
  // The two views that delete and then leave: once the route has changed,
  // nothing on the page still knows which entity is gone, so the cache work has
  // to happen first. Asserting on the source because demo mode is read-only.
  const paths: [string, string][] = [
    ['src/views/ServiceDetailView.tsx', 'orgApi.services.delete('],
    ['src/views/HostDetailView.tsx', 'orgApi.hosts.delete('],
  ];

  for (const [path, deleteCall] of paths) {
    const afterDelete = readFileSync(path, 'utf8').split(deleteCall)[1] ?? '';
    const cleared = afterDelete.indexOf('invalidateEntityDelete(');
    const navigated = afterDelete.indexOf('navigate(');
    expect(cleared, path).toBeGreaterThan(-1);
    expect(cleared, path).toBeLessThan(navigated);
  }
});

test('the serviceUpdated event bus is gone from the source', () => {
  // The event was the mechanism, not the goal: as long as one listener remains,
  // a write silently stops refreshing a list. It has to disappear whole.
  // Matches the event name as code (quoted), not the mentions in comments that
  // explain what replaced it.
  const used = /['"]serviceUpdated['"]/;
  const offenders: string[] = [];

  const walk = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = `${dir}/${entry.name}`;
      if (entry.isDirectory()) walk(path);
      else if (/\.tsx?$/.test(entry.name) && used.test(readFileSync(path, 'utf8'))) {
        offenders.push(path);
      }
    }
  };
  walk('src');

  expect(offenders).toEqual([]);
});
