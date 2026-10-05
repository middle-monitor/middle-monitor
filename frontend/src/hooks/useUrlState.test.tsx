import { act, cleanup, render } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';

import { useUrlState } from './useUrlState';

// vite.config.ts sets neither globals nor a setup file, so
// @testing-library/react never installs its own cleanup: without this, every
// render of the file stays mounted in document.body until the file ends.
afterEach(cleanup);

type State = { status: string; q: string };
const DEFAULTS: State = { status: 'all', q: '' };

// Drives the hook from a test and reports the URL after each write, which is
// the contract the views depend on: a shared link carries the filters, and the
// back button behaves the way a reader expects.
function harness(initialEntries: string[] = ['/services']) {
  const seen = { state: null as State | null, search: '' };
  let set: ReturnType<typeof useUrlState<State>>[1] | null = null;

  function Probe() {
    const [state, setState] = useUrlState<State>(DEFAULTS);
    const location = useLocation();
    set = setState;
    seen.state = state;
    seen.search = location.search;
    return null;
  }

  render(
    <MemoryRouter initialEntries={initialEntries}>
      <Probe />
    </MemoryRouter>
  );

  return {
    seen,
    write: (changes: Partial<State>) =>
      act(() => {
        set!(changes);
      }),
  };
}

describe('useUrlState', () => {
  it('starts from the defaults when the URL carries nothing', () => {
    const { seen } = harness();
    expect(seen.state).toEqual(DEFAULTS);
    expect(seen.search).toBe('');
  });

  // Opening a shared link has to reproduce the sender's list exactly.
  it('reads the state out of the URL', () => {
    const { seen } = harness(['/services?status=failing&q=api']);
    expect(seen.state).toEqual({ status: 'failing', q: 'api' });
  });

  it('writes a change into the URL', () => {
    const { seen, write } = harness();
    write({ status: 'failing' });
    expect(seen.search).toBe('?status=failing');
    expect(seen.state).toEqual({ status: 'failing', q: '' });
  });

  // A link should carry what the user chose, not the whole form. Going back to
  // a default has to remove the key rather than pin it, or every shared URL
  // grows a tail of noise that also freezes the default if it ever changes.
  it('keeps defaults out of the URL', () => {
    const { seen, write } = harness(['/services?status=failing']);
    write({ status: 'all' });
    expect(seen.search).toBe('');
  });

  it('leaves the other keys alone when one changes', () => {
    const { seen, write } = harness(['/services?status=failing']);
    write({ q: 'api' });
    expect(seen.search).toContain('status=failing');
    expect(seen.search).toContain('q=api');
  });

  // undefined means "do not touch this key", which is what lets a caller pass a
  // partial object without wiping the rest of the filters.
  it('ignores an undefined value instead of clearing the key', () => {
    const { seen, write } = harness(['/services?status=failing']);
    write({ q: undefined });
    expect(seen.search).toBe('?status=failing');
  });

  // Successive writes converge on the last value rather than accumulating.
  // Whether each one replaces or pushes a history entry is not observable from
  // MemoryRouter, so the back-button contract itself is pinned by the e2e suite
  // ("ten keystrokes in a search box stay one back step from the previous
  // view"); asserting it here would only look like coverage.
  it('converges on the last value written', () => {
    const { seen, write } = harness();
    write({ q: 'a' });
    write({ q: 'ap' });
    write({ q: 'api' });
    expect(seen.search).toBe('?q=api');
  });

  // Callers pass a literal rebuilt on every render. The hook keeps the first
  // one, so what counts as a default cannot drift under a view mid-session: a
  // value that was omitted from the URL stays omitted, and one that was written
  // stays written, whatever a later render claims the default is.
  it('freezes the defaults it was first given', () => {
    const seen = { search: '' };
    let set: ((changes: Partial<State>) => void) | null = null;
    let renders = 0;

    function Probe() {
      renders += 1;
      // The default for status flips after the first render.
      const [, setState] = useUrlState<State>({
        status: renders > 1 ? 'failing' : 'all',
        q: '',
      });
      const location = useLocation();
      set = setState;
      seen.search = location.search;
      return null;
    }

    render(
      <MemoryRouter initialEntries={['/services']}>
        <Probe />
      </MemoryRouter>
    );

    act(() => set!({ q: 'api' }));
    expect(renders).toBeGreaterThan(1);

    // "failing" is the later default, never the first one, so it must still be
    // written to the URL rather than stripped as a default.
    act(() => set!({ status: 'failing' }));
    expect(seen.search).toContain('status=failing');
  });
});
