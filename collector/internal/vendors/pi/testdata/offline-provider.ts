import { createAssistantMessageEventStream } from '__PI_AI_EVENT_STREAM__';

export default function (pi) {
  pi.registerCommand('probe-wait', { description: 'Open an isolated waiting-state probe', handler: async (_args, ctx) => {
    const answer = await ctx.ui.confirm('Pi status probe', 'Choose Yes or No to complete the waiting-state probe.');
    ctx.ui.notify('Probe finished: ' + String(answer), 'info');
  }});
  pi.registerProvider('coslash-probe', {
    api: 'coslash-probe-api', baseUrl: 'http://127.0.0.1/unused', apiKey: 'probe-only',
    models: [{ id: 'probe', name: 'Deterministic local probe', reasoning: false, input: ['text'],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 8192, maxTokens: 1024 }],
    streamSimple: (model, _context, options) => {
      const stream = createAssistantMessageEventStream();
      const message = { role: 'assistant', content: [], api: model.api, provider: model.provider, model: model.id,
        usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: 'stop', timestamp: Date.now() };
      stream.push({ type: 'start', partial: message });
      const finish = () => {
        if (options?.signal?.aborted) {
          message.stopReason = 'aborted'; message.errorMessage = 'Probe cancelled';
          stream.push({ type: 'error', reason: 'aborted', error: message });
        } else {
          message.content = [{ type: 'text', text: 'Deterministic local probe finished.' }];
          stream.push({ type: 'done', reason: 'stop', message });
        }
        stream.end();
      };
      const timer = setTimeout(finish, 2500);
      options?.signal?.addEventListener('abort', () => { clearTimeout(timer); finish(); }, { once: true });
      return stream;
    },
  });
}
