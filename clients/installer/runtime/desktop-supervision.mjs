import { uptime } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';

function requestStop(child) {
  if (!child.connected) return false;
  child.send({ aurigaDesktop: 'stop' }, () => {});
  return true;
}

// A supervisor-initiated stop is a restart within this same Desktop session,
// not an application exit. Wait for reconciliation to finish before deciding.
export async function superviseDsh({
  start,
  tree,
  supervisor,
  session,
  notify,
  attach = async () => {},
  stopSignal,
  poll = () =>
    stopSignal
      ? delay(1000, undefined, { signal: stopSignal }).catch(() => {})
      : delay(1000),
  now = () => Math.floor(uptime() * 1000),
  stopRequested = () => false,
  graceMs = 5000,
}) {
  for (;;) {
    // An exit requested by the owner (tray) is a clean application exit.
    if (stopRequested()) return 0;
    const child = start();
    let ended = false;
    const exited = new Promise((resolve) => {
      child.once('error', (error) => {
        ended = true;
        resolve({ error });
      });
      child.once('exit', (code) => {
        ended = true;
        resolve({ code: code ?? 1 });
      });
    });
    if (child.pid) {
      tree.attach(child.pid);
      session.child(child.pid);
      await attach(child);
    }
    let restart = false,
      refreshed = 0;
    while (!ended) {
      await poll();
      // Injected/event-based pollers may finish entirely in microtasks. Yield so
      // the child's exit event is delivered before another synchronous scan.
      await delay(0);
      if (ended) break;
      if (stopRequested()) break;
      if (now() - refreshed >= 15000) {
        tree.refresh();
        refreshed = now();
      }
      const outcome = await supervisor.reconcile();
      restart = Boolean(outcome?.restart || supervisor.restartPending);
      if (restart) {
        supervisor.restartPending = false;
        break;
      }
    }
    let forced = false;
    if (!ended && stopRequested()) {
      tree.refresh();
      if (requestStop(child))
        await Promise.race([exited, delay(graceMs, undefined, { ref: false })]);
      if (!ended) {
        forced = true;
        console.error('DSH 未在正常停止窗口内退出，正在清理剩余进程。');
        await tree.stopTree();
      }
    }
    const result = await exited;
    if (result.error) throw result.error;
    if (!forced) await tree.stopTree();
    if (stopRequested()) return forced ? 1 : result.code;
    if (!restart) return result.code;
    notify();
  }
}
