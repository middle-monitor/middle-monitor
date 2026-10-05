/**
 * Parse panic / runtime error messages from any Middle-Monitor SDK (Go, Python, Node, Rust)
 * and return a human-readable explanation for the "Context / Correlation" section.
 * No LLM required: patterns are deterministic and language-specific.
 *
 * Covered cases (non-exhaustive; generic fallback for unknown messages):
 * - Go: index/slice bounds, nil pointer, nil map write, close of (closed|nil) channel,
 *       send on closed channel, integer divide by zero, interface conversion (type assertion).
 * - Python: IndexError, KeyError, TypeError, AttributeError, ZeroDivisionError, ValueError,
 *           RecursionError, RuntimeError, OSError.
 * - JS/TS: TypeError (undefined/null), RangeError, ReferenceError.
 * - Rust: panic at / panicked at (message extracted).
 *
 * Runtime is inferred from the error shape, then refined with the source file
 * extension: a JS/TS error coming from a frontend file (.tsx/.ts/.jsx/...) is
 * tagged as 'browser' (shown as "UI"), not "Node.js".
 */

export interface PanicExplanation {
  isPanic: boolean;
  /** Short label, e.g. "Index out of range" (runtime shown via badge) */
  label: string;
  /** One-sentence cause in English */
  cause: string;
  /** Optional suggestion to fix */
  suggestion?: string;
  /** Inferred runtime/language for display */
  runtime?: 'go' | 'python' | 'browser' | 'node' | 'rust' | 'unknown';
}

/** Frontend source extensions: a JS/TS error from such a file is UI, not Node. */
function isBrowserFile(file?: string): boolean {
  if (!file) return false;
  return /\.(tsx?|jsx?|mjs|cjs|vue|svelte)$/i.test(file.trim());
}

/** Detect panic and parse cause from error name + message (+ optional file) */
export function parsePanicExplanation(name: string, message: string, file?: string): PanicExplanation | null {
  const n = (name || '').toLowerCase();
  const m = (message || '').trim();

  // ----- Go: name is "panic" — try to parse message (runtime error, nil map, etc.)
  if (n === 'panic') {
    const go = parseGoPanic(m);
    if (go) return { ...go, runtime: 'go' };
    return {
      isPanic: true,
      label: 'Panic',
      cause: m || 'Panic with no message.',
      suggestion: 'Check the logs and the stack trace for the exact line.',
      runtime: 'go',
    };
  }
  // ----- Python: classic exception names in message or name
  const py = parsePythonPanic(n, m);
  if (py) return { ...py, runtime: 'python' };

  // ----- JS/TS: TypeError, RangeError, etc. in message. A frontend source file
  // means it is browser/UI code, not Node.js.
  const node = parseNodePanic(name, m);
  if (node) return { ...node, runtime: isBrowserFile(file) ? 'browser' : 'node' };

  // ----- Rust: "panic" in message or "panic at"
  if (m.includes('panic at') || m.includes('panicked at')) {
    const rust = parseRustPanic(m);
    return { ...rust, runtime: 'rust' };
  }

  return null;
}

function parseGoPanic(message: string): Omit<PanicExplanation, 'runtime'> | null {
  // index out of range [3] with length 1
  const indexMatch = message.match(/runtime error: index out of range \[(\d+)\] with length (\d+)/i);
  if (indexMatch) {
    const index = indexMatch[1];
    const length = indexMatch[2];
    return {
      isPanic: true,
      label: 'Index out of range',
      cause: `Access to index ${index} of a slice or array of length ${length}.`,
      suggestion: 'Check the bounds (len/range) before accessing elements.',
    };
  }

  // slice bounds out of range
  if (/runtime error: slice bounds out of range/i.test(message)) {
    return {
      isPanic: true,
      label: 'Slice bounds out of range',
      cause: 'Slicing with invalid indices (start, end or capacity).',
      suggestion: 'Make sure the indices are within [0, len(slice)].',
    };
  }

  // nil pointer dereference
  if (/runtime error: invalid memory address or nil pointer dereference/i.test(message)) {
    return {
      isPanic: true,
      label: 'Nil pointer',
      cause: 'Dereference of a nil pointer.',
      suggestion: 'Check that the pointer is not nil before using it.',
    };
  }

  // assignment to entry in nil map
  if (/assignment to entry in nil map/i.test(message)) {
    return {
      isPanic: true,
      label: 'Uninitialized map',
      cause: 'Attempt to write to an uninitialized (nil) map.',
      suggestion: 'Initialize the map with make(map[string]int) (or the right type) before writing.',
    };
  }

  // close of closed channel
  if (/close of closed channel/i.test(message)) {
    return {
      isPanic: true,
      label: 'Channel already closed',
      cause: 'close() called on an already closed channel.',
      suggestion: 'Close a channel only once (e.g. a single dedicated goroutine, or sync.Once).',
    };
  }

  // close of nil channel
  if (/close of nil channel/i.test(message)) {
    return {
      isPanic: true,
      label: 'Close of nil channel',
      cause: 'close() called on an uninitialized (nil) channel.',
      suggestion: 'Create the channel with make(chan T) before using or closing it.',
    };
  }

  // send on closed channel
  if (/send on closed channel/i.test(message)) {
    return {
      isPanic: true,
      label: 'Send on closed channel',
      cause: 'Attempt to send on an already closed channel.',
      suggestion: 'Do not close the channel while goroutines may still send, or coordinate with sync.WaitGroup.',
    };
  }

  // interface conversion (type assertion failure)
  if (/interface conversion:/i.test(message)) {
    return {
      isPanic: true,
      label: 'Type assertion failed',
      cause: message.split('\n')[0] || message,
      suggestion: 'Use the comma-ok idiom (v, ok := x.(T)) or a type switch.',
    };
  }

  // value method called on nil pointer (panicwrap)
  if (/value method .+ called using nil .+ pointer/i.test(message)) {
    return {
      isPanic: true,
      label: 'Method on nil receiver',
      cause: 'A value method was called on a nil pointer.',
      suggestion: 'Check that the receiver is not nil before the call.',
    };
  }

  // integer divide by zero
  if (/runtime error: integer divide by zero/i.test(message)) {
    return {
      isPanic: true,
      label: 'Divide by zero',
      cause: 'Integer division by zero.',
      suggestion: 'Check that the divisor is not zero before dividing.',
    };
  }

  // generic runtime error
  if (/^runtime error:/i.test(message)) {
    const rest = message.replace(/^runtime error:\s*/i, '').trim();
    return {
      isPanic: true,
      label: 'Runtime error',
      cause: rest || message,
      suggestion: 'Check the stack trace for the line involved.',
    };
  }

  return null;
}

function parsePythonPanic(name: string, message: string): Omit<PanicExplanation, 'runtime'> | null {
  const lower = message.toLowerCase();
  const nameLower = name.toLowerCase();

  if (nameLower === 'indexerror' || lower.includes('indexerror') || /index out of range/i.test(message)) {
    return {
      isPanic: true,
      label: 'IndexError',
      cause: 'Access to a non-existent index in a list or sequence.',
      suggestion: 'Check that the index is within range(len(seq)), or use .get() for dicts.',
    };
  }
  if (nameLower === 'keyerror' || lower.includes('keyerror')) {
    const keyMatch = message.match(/KeyError:\s*['"]?([^'"]+)['"]?/);
    const key = keyMatch ? keyMatch[1] : '?';
    return {
      isPanic: true,
      label: 'KeyError',
      cause: `Missing key: "${key}".`,
      suggestion: 'Check the key or use dict.get(key, default).',
    };
  }
  // TypeError is the one name Python and JavaScript share, and this parser runs
  // first, so without this guard it claimed every V8 TypeError and the JS
  // branch below was unreachable: a browser error was labelled Python and
  // offered a suggestion about None.
  if ((nameLower === 'typeerror' || lower.includes('typeerror')) && !isV8TypeError(message)) {
    return {
      isPanic: true,
      label: 'TypeError',
      cause: message.split('\n')[0] || 'Operation on an incorrect type.',
      suggestion: 'Check the argument types (None, expected type).',
    };
  }
  if (nameLower === 'attributeerror' || lower.includes('attributeerror')) {
    return {
      isPanic: true,
      label: 'AttributeError',
      cause: 'Access to a non-existent attribute or method (often a None object).',
      suggestion: 'Check that the object is not None and that the attribute exists.',
    };
  }
  if (nameLower === 'zerodivisionerror' || lower.includes('zerodivisionerror')) {
    return {
      isPanic: true,
      label: 'ZeroDivisionError',
      cause: 'Division by zero.',
      suggestion: 'Check that the divisor is not zero.',
    };
  }
  if (nameLower === 'valueerror' || lower.includes('valueerror')) {
    return {
      isPanic: true,
      label: 'ValueError',
      cause: message.split('\n')[0] || 'Invalid value for the operation.',
      suggestion: 'Check the format or the range of the value.',
    };
  }
  if (nameLower === 'recursionerror' || lower.includes('recursionerror') || /maximum recursion depth/i.test(message)) {
    return {
      isPanic: true,
      label: 'RecursionError',
      cause: 'Recursion depth limit exceeded.',
      suggestion: 'Check the recursive calls (base case), or raise sys.setrecursionlimit() if legitimate.',
    };
  }
  if (nameLower === 'runtimeerror' || lower.includes('runtimeerror')) {
    return {
      isPanic: true,
      label: 'RuntimeError',
      cause: message.split('\n')[0] || 'Error at runtime.',
      suggestion: 'Check the message and the stack trace for the cause.',
    };
  }
  if (nameLower === 'oserror' || nameLower === 'ioerror' || lower.includes('oserror') || lower.includes('ioerror')) {
    return {
      isPanic: true,
      label: 'OSError',
      cause: message.split('\n')[0] || 'System error (file, network, etc.).',
      suggestion: 'Check paths, permissions and resource availability.',
    };
  }
  if (nameLower === 'assertionerror' || lower.includes('assertionerror')) {
    return {
      isPanic: true,
      label: 'AssertionError',
      cause: message.split('\n')[0] || 'An assertion failed.',
      suggestion: 'Check the assert condition or the preconditions.',
    };
  }
  if (nameLower === 'modulenotfounderror' || nameLower === 'importerror' || lower.includes('modulenotfounderror') || lower.includes('importerror')) {
    return {
      isPanic: true,
      label: 'ImportError',
      cause: message.split('\n')[0] || 'Module not found or import error.',
      suggestion: 'Check the module name, the PYTHONPATH or the virtual environment.',
    };
  }
  return null;
}

/** V8-only TypeError wording; Python never phrases a TypeError this way. */
function isV8TypeError(message: string): boolean {
  return /Cannot read propert(y|ies)/i.test(message)
    || /undefined is not/i.test(message)
    || /null is not an object/i.test(message)
    || /is not a function/i.test(message);
}

function parseNodePanic(name: string, message: string): Omit<PanicExplanation, 'runtime'> | null {
  if (/TypeError:\s*Cannot read propert(y|ies)/i.test(message) || /undefined is not/i.test(message)) {
    return {
      isPanic: true,
      label: 'TypeError',
      cause: 'Reading a property on undefined or null.',
      suggestion: 'Check that the object exists (optional chaining ?. or a guard).',
    };
  }
  if (/RangeError:\s*Invalid array length/i.test(message) || /RangeError:\s*index out of range/i.test(message)) {
    return {
      isPanic: true,
      label: 'RangeError',
      cause: 'Index out of range or invalid array length.',
      suggestion: 'Check the bounds before accessing or creating arrays.',
    };
  }
  if (/ReferenceError:\s*.+ is not defined/i.test(message) || name === 'ReferenceError') {
    return {
      isPanic: true,
      label: 'ReferenceError',
      cause: 'Variable or reference not defined in the current scope.',
      suggestion: 'Check that the variable is declared (var/let/const), imported and in scope.',
    };
  }
  if (name === 'RangeError' || name === 'TypeError' || name === 'ReferenceError') {
    return {
      isPanic: true,
      label: name,
      cause: message.split('\n')[0] || message,
      suggestion: 'Check the stack trace for the line involved.',
    };
  }
  return null;
}

function parseRustPanic(message: string): Omit<PanicExplanation, 'runtime'> {
  const atMatch = message.match(/panicked at\s+['"]([^'"]+)['"]/);
  const detail = atMatch ? atMatch[1] : message.split('\n')[0] || message;
  return {
    isPanic: true,
    label: 'Panic',
    cause: detail,
    suggestion: 'Check unwrap(), expect() and assertions (assert!, panic!).',
  };
}
