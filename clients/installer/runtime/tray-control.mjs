/**
 * Tray-owned launcher control. The tray writes one command per line to the
 * launcher's stdin: "stop" (or the end of stdin, e.g. the tray exited or
 * crashed) stops DSH cleanly; "login-fallback" explicitly requests browser recovery.
 * Status events go to stdout as `@@auriga <json>` lines without secrets.
 */
export function createControl({
  input = process.stdin,
  write = (text) => process.stdout.write(text),
} = {}) {
  let requested = false;
  const controller = new AbortController();
  const handlers = new Map();
  const stop = () => {
    if (requested) return;
    requested = true;
    controller.abort();
  };
  let buffer = '';
  input.setEncoding?.('utf8');
  input.on('data', (chunk) => {
    buffer += chunk;
    const lines = buffer.split('\n');
    buffer = lines.pop().slice(-256);
    for (const raw of lines) {
      const command = raw.trim();
      if (command === 'stop') stop();
      else handlers.get(command)?.();
    }
  });
  input.on('end', stop);
  input.on('close', stop);
  input.on('error', stop);
  return {
    get requested() {
      return requested;
    },
    get signal() {
      return controller.signal;
    },
    on(command, handler) {
      handlers.set(command, handler);
    },
    report(event) {
      write(`@@auriga ${JSON.stringify(event)}\n`);
    },
  };
}
