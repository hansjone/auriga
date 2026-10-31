/* global location */
// Desktop local admission is owned by DSH. Only Web mode needs a login gate.
(async () => {
  if (location.protocol === 'dsh-app:') return;
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const settings = await fetch('/auriga/settings/state', {
      credentials: 'same-origin',
    });
    if (settings.ok) {
      const preferences = await settings.json();
      if (!preferences.platformOrigin) return;
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
