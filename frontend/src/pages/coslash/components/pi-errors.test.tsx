import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { PiErrorBadge } from '@/pages/coslash/components/SessionInspector';
import { decodeSession } from '@/pages/coslash/hooks/use-sessions';

describe('Pi inspector error badge', () => {
  it('retains the local diagnostic and exposes a focusable badge', () => {
    const diagnostic = 'Refresh SSO credentials <script>';
    const session = decodeSession({ agent: 'pi', agentError: diagnostic });
    const markup = renderToStaticMarkup(<PiErrorBadge diagnostic={session.agentError} />);
    expect(markup).toContain('type="button"');
    expect(markup).toContain('aria-label="Pi error: Refresh SSO credentials &lt;script&gt;"');
    expect(markup).toContain('data-variant="destructive"');
    expect(markup).toContain('>Error</button>');
    expect(markup).not.toContain('<script>');
  });

  it('omits the badge when no provider failure is recorded', () => {
    expect(renderToStaticMarkup(<PiErrorBadge />)).toBe('');
    expect(renderToStaticMarkup(<PiErrorBadge diagnostic={null} />)).toBe('');
  });
});
