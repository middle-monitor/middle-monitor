import type { Reporter, TestCase, TestResult } from '@playwright/test/reporter';

// Prints the outcome of refresh-state.spec.ts in the CI job's own output, so
// the recette does not need the HTML report, let alone a local run: one line
// per view, then the 75th percentile of the INP values against the 200 ms
// reference threshold of the epic design (decision 8).
//
// The threshold is a reference, not a gate: a shared CI runner times an
// interaction far less reliably than a real browser on a real machine, so a
// value over it is worth reading, not worth failing the pipeline on.
const THRESHOLD_MS = 200;

function percentile75(values: number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.ceil(sorted.length * 0.75) - 1];
}

class InpReporter implements Reporter {
  // Keyed by view, so a retried test reports the run that counted. A test that
  // fails before its last instruction attaches nothing and is absent here; the
  // `list` reporter is what says a view failed.
  private measured = new Map<string, number>();

  onTestEnd(_test: TestCase, result: TestResult) {
    const attachment = result.attachments.find((a) => a.name === 'inp');
    if (!attachment?.body) return;
    const { view, inpMs } = JSON.parse(attachment.body.toString()) as {
      view: string;
      inpMs: number;
    };
    this.measured.set(view, inpMs);
  }

  onEnd() {
    if (this.measured.size === 0) return;

    const lines = [`Refresh-state net — INP per view (reference ${THRESHOLD_MS} ms)`];
    for (const [view, inpMs] of this.measured) {
      // Zero is "no interaction crossed the 16 ms reporting floor", not "free".
      const value = inpMs > 0 ? `${Math.round(inpMs)} ms` : '< 16 ms';
      lines.push(`  ${view.padEnd(20)} ${value.padStart(8)}`);
    }

    const p75 = Math.round(percentile75([...this.measured.values()]));
    const verdict = p75 <= THRESHOLD_MS ? 'within reference' : 'over reference';
    lines.push(`  INP p75: ${p75} ms (${verdict})`);

    console.log(lines.join('\n'));
  }
}

export default InpReporter;
