// @ts-nocheck
/* global document, TextDecoder */
/** Real native UI transport. No identity, token or owner credential is stored
 * in the browser; each same-origin call is admitted again by Edge and Host. */
export function installDedicatedBrowser() {
  const base = new URL('./', document.baseURI);
  const fetcher = globalThis.fetch.bind(globalThis);
  async function send(kind, channel, endpoint, payload, signal) {
    if (channel !== '/api') throw new Error('独立实例接口不可用');
    const response = await fetcher(
      new URL(`auriga/dedicated/browser/${kind}`, base),
      {
        method: 'POST',
        credentials: 'same-origin',
        cache: 'no-store',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ channel, endpoint, payload }),
        signal,
      },
    );
    if (!response.ok)
      throw new Error(`独立实例连接不可用 (${response.status})`);
    return response;
  }
  globalThis.__DSH_TRANSPORT__ = {
    ownsHost: true,
    rpc: {
      async call(channel, endpoint, payload, signal) {
        const result = await (
          await send('rpc', channel, endpoint, payload, signal)
        ).json();
        signal?.throwIfAborted();
        if (typeof result?.ok !== 'boolean')
          throw new Error('独立实例响应无效');
        return result;
      },
      async *open(channel, endpoint, payload, signal) {
        const response = await send(
          'stream',
          channel,
          endpoint,
          payload,
          signal,
        );
        if (!response.body) throw new Error('独立实例流不可用');
        const reader = response.body.getReader(),
          decoder = new TextDecoder();
        let buffer = '',
          ended = false;
        try {
          while (!ended) {
            const part = await reader.read();
            signal?.throwIfAborted();
            if (part.done) throw new Error('独立实例流已中断');
            buffer += decoder.decode(part.value, { stream: true });
            if (buffer.length > 8388608) throw new Error('独立实例流帧过大');
            let at;
            while ((at = buffer.indexOf('\n')) !== -1) {
              const frame = JSON.parse(buffer.slice(0, at));
              buffer = buffer.slice(at + 1);
              signal?.throwIfAborted();
              if (frame.type === 'item') yield frame.value;
              else if (frame.type === 'end') {
                ended = true;
                break;
              } else if (frame.type === 'error') {
                const error = new Error(
                  frame.error?.message ?? '独立实例流不可用',
                );
                error.dshRemoteStreamFailure = {
                  kind: 'remote',
                  code: frame.error?.code ?? 'shared.revoked',
                  details: frame.error?.details ?? {},
                };
                throw error;
              } else throw new Error('独立实例流帧无效');
            }
          }
        } finally {
          await reader.cancel().catch(() => {});
          reader.releaseLock();
        }
      },
    },
  };
}
export const dedicatedBootstrap = `(${installDedicatedBrowser.toString()})();`;
