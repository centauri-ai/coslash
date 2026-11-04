import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { Dialog } from '@/components/ui/dialog';
import { HubSwitchDialogContent } from './HubSwitchDialog';

const request = {
  from: 'https://hub.example.test',
  to: 'https://staging.example.test',
  canSwitch: true,
  requestedAt: '2026-11-04T12:00:00Z',
};

const render = (props: Partial<Parameters<typeof HubSwitchDialogContent>[0]>) =>
  renderToStaticMarkup(
    <Dialog>
      <HubSwitchDialogContent
        request={request}
        pending={false}
        error={null}
        onResolve={() => {}}
        {...props}
      />
    </Dialog>,
  );

describe('HubSwitchDialogContent', () => {
  it('offers to keep or switch when the other Hub can take over', () => {
    const markup = render({});
    expect(markup.replace(/<[^>]+>/g, '')).toContain(
      'This computer syncs to hub.example.test. staging.example.test asked to connect it.',
    );
    expect(markup).toContain('Keep hub.example.test');
    expect(markup).toContain('Switch to staging.example.test');
  });

  it('points to setup in the other Hub when it cannot take over', () => {
    const markup = render({ request: { ...request, canSwitch: false } });
    expect(markup.replace(/<[^>]+>/g, '')).toContain('open staging.example.test and choose');
    expect(markup).not.toContain('Switch to staging.example.test');
  });

  it('shows a failed switch as an alert', () => {
    expect(render({ error: 'coSlash Local could not switch Hubs. Try again.' })).toContain('role="alert"');
  });
});
