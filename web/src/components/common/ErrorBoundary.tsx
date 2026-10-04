import React from 'react';

interface ErrorBoundaryState {
  failed: boolean;
}

function fallbackCopy() {
  const zh = typeof document === 'undefined' || !document.documentElement.lang.toLowerCase().startsWith('en');
  return zh
    ? { title: '页面出错了', description: '刷新页面通常可以恢复。如果问题反复出现，请稍后再试。', action: '刷新页面' }
    : { title: 'Something went wrong', description: 'Reloading the page usually fixes this. If it keeps happening, please try again later.', action: 'Reload page' };
}

// Last line of defence: a render error anywhere below must not leave a blank page.
// Deliberately self-contained (no context or router) so it still works if a provider is what failed.
export class ErrorBoundary extends React.Component<{ children: React.ReactNode }, ErrorBoundaryState> {
  state: ErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    console.error('Unhandled render error', error, info.componentStack);
  }

  render() {
    if (!this.state.failed) return this.props.children;
    const copy = fallbackCopy();
    return (
      <div role="alert" style={{ minHeight: '100vh', display: 'grid', placeItems: 'center', padding: 24, textAlign: 'center', fontFamily: 'system-ui, sans-serif' }}>
        <div style={{ maxWidth: 420 }}>
          <h1 style={{ fontSize: 22, margin: '0 0 8px' }}>{copy.title}</h1>
          <p style={{ margin: '0 0 20px', color: '#5b6650' }}>{copy.description}</p>
          <button type="button" onClick={() => window.location.reload()} style={{ padding: '10px 20px', borderRadius: 999, border: 0, background: '#b9f600', fontWeight: 700, cursor: 'pointer' }}>
            {copy.action}
          </button>
        </div>
      </div>
    );
  }
}
