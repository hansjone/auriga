// Private subprocess entry for an independently installed plugin. All platform
// authentication stays in C's identical packaged artifact, outside the DSH VM.
import { Writable } from 'node:stream';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { join } from 'node:path';
import { verifyBrokerPackage } from './broker-package.js';

async function main() {
  if (!process.send || process.platform !== 'win32')
    throw new Error('private broker launcher unavailable');
  const root = process.argv[2],
    dataHome = process.argv[3];
  if (!root || !dataHome) throw new Error('broker installation required');
  const artifact = await verifyBrokerPackage(
    fileURLToPath(new URL('../../shell-auth/', import.meta.url)),
  );
  const { startDesktopAuth } = await import(
    pathToFileURL(join(artifact.base, 'desktop.mjs')).href
  );
  const auth = await startDesktopAuth({ root, dataHome });
  if (!auth) throw new Error('broker installation required');
  let closing = false;
  const close = async () => {
    if (closing) return;
    closing = true;
    await auth.close();
    process.disconnect?.();
  };
  process.once('disconnect', () => {
    void close().catch(() => {});
  });
  process.on('message', (message) => {
    if (/** @type {any} */ (message)?.action === 'close')
      void close().catch(() => {});
  });
  try {
    // C records the actual parent's OS process start time. Its helper later
    // rechecks SID and this controlled process tree for every connection.
    await auth.attach({
      pid: process.ppid,
      stdio: [
        null,
        null,
        null,
        new Writable({
          write(chunk, _encoding, done) {
            try {
              process.send?.({
                descriptor: JSON.parse(chunk.toString('utf8')),
              });
              done();
            } catch {
              done(new Error('private broker descriptor invalid'));
            }
          },
        }),
      ],
    });
  } catch (error) {
    await close();
    throw error;
  }
}
// The private parent receives only a fixed classification, never C's exception
// messages, credentials, platform response bodies or helper output.
void main().catch(() => {
  process.send?.({ unavailable: true });
  process.exitCode = 1;
  process.disconnect?.();
});
