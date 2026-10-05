import { Component, type ErrorInfo, type ReactNode } from 'react';
import { HiOutlineExclamationTriangle } from 'react-icons/hi2';
import { captureError } from '@middle-monitor/web';

interface Props {
  children: ReactNode;
  /** Optional custom fallback. Receives the error and a reset callback. */
  fallback?: (error: Error, reset: () => void) => ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Catches render-time errors anywhere in the subtree and shows a recoverable
 * fallback instead of unmounting the whole app (white screen). React does not
 * forward boundary-caught errors to window.onerror, so the SDK's global handler
 * never sees them: componentDidCatch reports them with captureError.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Render error caught by ErrorBoundary:', error, info.componentStack);
    captureError(error, {
      file: info.componentStack?.trim().split('\n')[0] || window.location.href,
    });
  }

  reset = () => this.setState({ error: null });

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;

    if (this.props.fallback) return this.props.fallback(error, this.reset);

    return (
      <div
        style={{
          minHeight: '100vh',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          justifyContent: 'center',
          gap: '1rem',
          padding: '2rem',
          textAlign: 'center',
          background: 'var(--bg-primary, #09090b)',
          color: 'var(--text-primary, #f4f4f5)',
        }}
      >
        <div style={{ fontSize: '2.5rem', color: 'var(--status-warning, #eab308)', lineHeight: 1 }}>
          <HiOutlineExclamationTriangle />
        </div>
        <h1 style={{ fontSize: '1.25rem', fontWeight: 700, margin: 0 }}>
          Something went wrong
        </h1>
        <p style={{ color: 'var(--text-secondary, #a1a1aa)', maxWidth: 480, margin: 0 }}>
          An unexpected error occurred while rendering this page. The issue has been
          logged. You can retry or reload the application.
        </p>
        <div style={{ display: 'flex', gap: '0.75rem', marginTop: '0.5rem' }}>
          <button
            onClick={this.reset}
            style={{
              padding: '0.6rem 1.25rem',
              borderRadius: 8,
              border: '1px solid var(--border-primary, rgba(255,255,255,0.15))',
              background: 'transparent',
              color: 'var(--text-primary, #f4f4f5)',
              fontWeight: 600,
              cursor: 'pointer',
            }}
          >
            Try again
          </button>
          <button
            onClick={() => window.location.assign('/')}
            style={{
              padding: '0.6rem 1.25rem',
              borderRadius: 8,
              border: 'none',
              background: 'var(--brand-primary, #8b5cf6)',
              color: '#fff',
              fontWeight: 600,
              cursor: 'pointer',
            }}
          >
            Go home
          </button>
        </div>
      </div>
    );
  }
}
