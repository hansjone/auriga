/* global location */
// Runs through the native structured index injection before the application
// requires its authenticated RPC/stream transport. The native shell's HTTP
// welcome probe does not execute scripts and can still read its static index.
(async () => {
  const deadline = Date.now() + 30000;
  const native = location.protocol === 'dsh-app:';
  let requestId = 0;
  /** @param {string} action @param {object} [args] */
  const rpc = async (action, args = {}) => {
    const method = 'aurigaAuth/' + action;
    const rpcId = 'auriga-startup-' + ++requestId;
    const response = await fetch('/auriga-native/' + method, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        type: 'client-request',
        rpcId,
        method,
        payload: { args },
      }),
    });
    const envelope = await response.json();
    if (
      !response.ok ||
      envelope.type !== 'server-response' ||
      envelope.rpcId !== rpcId
    )
      throw new Error('native login status unavailable');
    return envelope.result;
  };
  while (Date.now() < deadline) {
    const settings = await fetch('/auriga/settings/state', {
      credentials: 'same-origin',
    });
    if (settings.ok) {
      const preferences = await settings.json();
      if (!preferences.platformOrigin) return;
      if (native) {
        let result = await rpc('status');
        if (result.ok) {
          let status = result.value;
          if (!status.loggedIn && status.resumeAvailable) {
            const resumed = await rpc('login', { input: { action: 'resume' } });
            if (resumed.ok) {
              result = await rpc('status');
              if (!result.ok)
                throw new Error('native login status unavailable');
              status = result.value;
              if (status.loggedIn === true) {
                // The first frontend transport may have connected before the
                // saved device was verified. Reload once with native admission
                // already bound; the next status is logged in and cannot loop.
                location.replace('/');
                return;
              }
            }
          }
          if (status.loggedIn === false)
            location.replace('/auriga/local-login');
          return;
        }
        if (result.error?.code !== 'common.unavailable') return;
        await new Promise((resolve) => setTimeout(resolve, 200));
        continue;
      }
      const response = await fetch('/auriga/local-login/status', {
        credentials: 'same-origin',
      });
      if (response.ok) {
        const status = await response.json();
        if (status.loggedIn === false) location.replace('/auriga/local-login');
        return;
      }
      if (response.status !== 503) return;
    } else if (settings.status !== 503) return;
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
})().catch(() => {});
