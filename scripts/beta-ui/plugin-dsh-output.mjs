// Detached log relay. Raw host output and launch URLs remain in memory only.
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { createHostOutput } from './plugin-dsh.mjs';
import { privatePath, processIdentity } from './runner.mjs';

const [state, bin] = process.argv.slice(2);
let child, output;
const send = (message) => {
  if (process.connected) process.send(message, () => {});
};
try {
  // The launcher already claimed and checked StateDir against the real user
  // environment. Here HOME/DSH_HOME intentionally point inside that directory.
  if (!path.isAbsolute(state)) throw new Error('Absolute StateDir required');
  privatePath(state);
  output = fs.openSync(path.join(state, 'dsh.log'), 'wx', 0o600);
  const worker = { pid: process.pid, identity: processIdentity(process.pid) };
  if (!worker.identity) throw new Error('Cannot identify log relay');
  child = spawn(
    process.execPath,
    [
      bin,
      '--profile',
      'web',
      '--host',
      '127.0.0.1',
      '--port',
      '0',
      '--no-open',
    ],
    {
      cwd: path.join(state, 'workspace'),
      env: process.env,
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  for (const stream of [child.stdout, child.stderr]) {
    const capture = createHostOutput(
      (line) => fs.writeSync(output, line),
      (url) => send({ type: 'url', url }),
    );
    stream.on('data', (chunk) => capture.write(chunk));
    stream.once('end', () => capture.end());
  }
  await new Promise((resolve, reject) => {
    child.once('spawn', resolve);
    child.once('error', reject);
  });
  const host = { pid: child.pid, identity: processIdentity(child.pid) };
  if (!host.identity) throw new Error('Cannot identify host');
  fs.writeFileSync(
    path.join(state, 'host-process.json'),
    JSON.stringify({ worker, host }),
    { mode: 0o600 },
  );
  send({ type: 'started', host });
  child.once('close', (code) => {
    fs.closeSync(output);
    send({ type: 'exit' });
    if (process.connected) process.disconnect();
    process.exitCode = code ?? 1;
  });
} catch {
  child?.kill();
  child?.stdout.destroy();
  child?.stderr.destroy();
  if (output !== undefined) {
    fs.writeSync(output, 'Host output relay failed\n');
    fs.closeSync(output);
  }
  send({ type: 'failure' });
  if (process.connected) process.disconnect();
  process.exitCode = 1;
}
