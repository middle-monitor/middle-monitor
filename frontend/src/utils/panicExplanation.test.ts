import { describe, expect, it } from 'vitest';

import { parsePanicExplanation } from './panicExplanation';

// This is the rules-based half of the error panel: it names a crash without
// calling the LLM. What it must never do is guess a runtime wrong or claim a
// cause for something it did not recognise, because the reader acts on the
// sentence it produces.
describe('parsePanicExplanation', () => {
  describe('Go', () => {
    it('reads the index and length out of an out-of-range panic', () => {
      const p = parsePanicExplanation('panic', 'runtime error: index out of range [3] with length 1');
      expect(p?.label).toBe('Index out of range');
      expect(p?.runtime).toBe('go');
      // The numbers are the whole value of the sentence: they say how far off
      // the access was.
      expect(p?.cause).toContain('index 3');
      expect(p?.cause).toContain('length 1');
    });

    it('names a nil pointer dereference', () => {
      const p = parsePanicExplanation('panic', 'runtime error: invalid memory address or nil pointer dereference');
      expect(p?.label).toBe('Nil pointer');
      expect(p?.runtime).toBe('go');
    });

    it('names a write to an uninitialised map', () => {
      const p = parsePanicExplanation('panic', 'assignment to entry in nil map');
      expect(p?.label).toBe('Uninitialized map');
      expect(p?.suggestion).toContain('make(');
    });

    // An unrecognised panic is still a panic: it must be reported as one with
    // its own message rather than dropped.
    it('still reports an unrecognised panic', () => {
      const p = parsePanicExplanation('panic', 'something nobody has a rule for');
      expect(p?.isPanic).toBe(true);
      expect(p?.runtime).toBe('go');
      expect(p?.cause).toContain('something nobody has a rule for');
    });

    it('does not invent a message for a panic that has none', () => {
      const p = parsePanicExplanation('panic', '');
      expect(p?.isPanic).toBe(true);
      expect(p?.cause).toBe('Panic with no message.');
    });
  });

  describe('Python', () => {
    it('recognises the common exception names', () => {
      expect(parsePanicExplanation('IndexError', 'list index out of range')?.runtime).toBe('python');
      expect(parsePanicExplanation('KeyError', "'user_id'")?.runtime).toBe('python');
      expect(parsePanicExplanation('ZeroDivisionError', 'division by zero')?.runtime).toBe('python');
    });
  });

  // The runtime drives the badge next to the error. A TypeError from a .tsx
  // file is the user's browser, not the server, and sending a reader to the
  // wrong tier is worse than showing no badge at all.
  describe('runtime inference', () => {
    it('tags a JS error from a frontend file as browser', () => {
      const p = parsePanicExplanation('TypeError', "Cannot read properties of undefined (reading 'id')", 'src/views/HostsView.tsx');
      expect(p?.runtime).toBe('browser');
    });

    // Any JS/TS extension counts as frontend, including .js: the file name is
    // the only signal available, and a server-side .js is indistinguishable
    // from a bundled one. Worth pinning so the trade-off stays deliberate.
    it('tags any JS or TS extension as browser', () => {
      for (const file of ['a.ts', 'a.jsx', 'a.mjs', 'server/routes.js']) {
        expect(parsePanicExplanation('TypeError', "Cannot read properties of undefined (reading 'id')", file)?.runtime).toBe('browser');
      }
    });

    it('falls back to node when the file is unknown or not a JS source', () => {
      expect(parsePanicExplanation('TypeError', "Cannot read properties of undefined (reading 'id')", 'main.go')?.runtime).toBe('node');
    });

    it('tags it as node when no file is known', () => {
      const p = parsePanicExplanation('TypeError', "Cannot read properties of undefined (reading 'id')");
      expect(p?.runtime).toBe('node');
    });

    it('recognises a Rust panic', () => {
      const p = parsePanicExplanation('Error', "panicked at 'called `Option::unwrap()` on a `None` value'");
      expect(p?.runtime).toBe('rust');
      expect(p?.isPanic).toBe(true);
    });
  });

  // An ordinary application error is not a crash. Returning an explanation here
  // would put a "panic" badge on a plain validation failure.
  it('returns null for an error that is not a crash', () => {
    expect(parsePanicExplanation('ValidationError', 'email is required')).toBeNull();
    expect(parsePanicExplanation('', '')).toBeNull();
  });

  // Name and message come from stored rows and either can be absent.
  it('survives missing name or message', () => {
    expect(() => parsePanicExplanation(undefined as unknown as string, undefined as unknown as string)).not.toThrow();
  });
});
