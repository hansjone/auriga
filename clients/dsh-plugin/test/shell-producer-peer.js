import { mkdtemp, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { fixture, login, OWNER } from '../../shell-auth/test/fixtures.mjs';
import { createConnectRuntime } from '../src/local/runtime.js';
import { validate } from './broker-fixture.js';

// Actual C control producer and D consumer. Only IAM and private IPC peer
// authentication are replaced; no process, browser or external network starts.
export async function producerRuntime(t, options = {}) {
  const source = options.source ?? fixture(options.platform);
  await source.broker.initialize();
  if (options.loggedIn) await login(source);
  const directory = await mkdtemp(join(tmpdir(), 'auriga-producer-consumer-'));
  let unsubscribe;
  const runtime = await createConnectRuntime({
    config: { platformOrigin: '' },
    directory,
    ctx: {},
    connection: {
      validate,
      ipc: {
        call(command) {
          if (command.path || command.call)
            throw new Error('business operation not configured');
          return source.broker.command(command, OWNER);
        },
        subscribe(command, receive) {
          unsubscribe = source.broker.subscribe(command, OWNER, receive);
        },
        close() {
          unsubscribe?.();
        },
      },
    },
  });
  t.after(async () => {
    await runtime.close();
    await source.broker.close();
    await rm(directory, { recursive: true, force: true });
  });
  const request = runtime.nativeRequest({ principal: { kind: 'owner' } });
  return { source, runtime, request };
}
