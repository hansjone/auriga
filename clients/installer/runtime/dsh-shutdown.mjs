// Node's Windows kill() terminates directly. Deliver the signal to the pinned
// DSH CLI's own JS handler over its inherited private IPC channel instead.
if (process.channel) {
  process.once('message', (message) => {
    if (message?.aurigaDesktop !== 'stop') return;
    const shutdown = () => {
      if (!process.listenerCount('SIGTERM')) return false;
      process.emit('SIGTERM');
      return true;
    };
    if (shutdown()) return;
    const timer = setInterval(() => {
      if (shutdown()) clearInterval(timer);
    }, 50);
    timer.unref();
  });
  process.channel.unref();
}
