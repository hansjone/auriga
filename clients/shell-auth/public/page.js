/* Browser secrets live only in this document's memory. */
(async () => {
  const ticket = location.hash.slice(1);
  history.replaceState(null, '', '/');
  let session, snapshot;
  const status = document.getElementById('status');
  async function command(action) {
    const response = await fetch('/loopback/messages', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Auriga-Shell-Token': session.pageToken,
        'X-Auriga-Shell-CSRF': session.csrf,
        'X-Auriga-Shell-Nonce': session.clientNonce,
      },
      body: JSON.stringify({
        protocolVersion: 1,
        requestId: crypto.randomUUID(),
        shellEpoch:
          action === 'snapshot'
            ? '00000000-0000-0000-0000-000000000000'
            : session.shellEpoch,
        expectedRevision: action === 'snapshot' ? 0 : (snapshot?.revision ?? 0),
        action,
      }),
    });
    if (!response.ok) throw new Error('操作未完成，请重新从启动器打开认证页。');
    const result = await response.json();
    if (result.type === 'error')
      throw new Error('操作未完成，请稍后重试或重新打开认证页。');
    if (!result.snapshot) return; // ack is acceptance, not login completion
    snapshot = result.snapshot;
    document.getElementById('attempt').textContent = snapshot.deviceAttempt
      ? `用户码：${snapshot.deviceAttempt.userCode}；有效至 ${new Date(snapshot.deviceAttempt.expiresAt).toLocaleTimeString()}。请在自己刚发起的门户中逐字核对。`
      : '';
    status.textContent = {
      signed_out: '未登录，可继续使用本地 DSH。',
      authenticating: '请在系统浏览器中的门户完成登录并批准设备。',
      authenticated: '平台已登录，请返回当前 DSH。',
      unreachable: '平台暂不可达，可跳过使用本地 DSH。',
      revoked: '平台授权已撤销，请重新登录。',
      reauth_required: '请重新登录。',
    }[snapshot.state];
  }
  try {
    const response = await fetch('/loopback/exchange', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        ticket,
        clientNonce: btoa(
          String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32))),
        )
          .replaceAll('+', '-')
          .replaceAll('/', '_')
          .replaceAll('=', ''),
      }),
    });
    if (!response.ok) throw new Error();
    session = await response.json();
    await command('snapshot');
    for (const [id, action] of [
      ['login', 'open_login'],
      ['cancel', 'cancel_login'],
      ['skip', 'skip_login'],
    ]) {
      const button = document.getElementById(id);
      if (!(button instanceof HTMLButtonElement)) throw new Error();
      button.disabled = false;
      button.onclick = () =>
        command(action).catch((e) => {
          status.textContent = e.message;
        });
    }
    setInterval(
      () =>
        command('snapshot').catch(() => {
          status.textContent = '认证页连接已失效，请从启动器重新打开。';
        }),
      2000,
    );
  } catch {
    status.textContent =
      '认证页票据已失效。请关闭本页，并从 Auriga Desktop 启动器重新打开。';
  }
})();
