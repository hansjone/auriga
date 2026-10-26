/* global AbortSignal */
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createReadStream } from 'node:fs';
import {
  mkdir,
  mkdtemp,
  open,
  writeFile,
  stat,
  chmod,
  rename,
} from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { findNatsServer } from './nats-server.js';

// Same version and archive/executable pins as services/test-support/NatsServer.java.
// The test checks all entries against that existing lock; no production download.
const VERSION = '2.15.1';
const PINS = {
  'windows-amd64.zip': [
    '4f7edfa376750b2720463bd22766ed469a5f13f378a643097866fcd953cbf7e3',
    '308b0ac72d7079a19f7221bf2326a3c8abfe861be0034fd73d9da444ca3fe055',
  ],
  'windows-arm64.zip': [
    '888a62b665b3123edde84c2e39751e4d9ced1e0dc7a7a234a40c53f2bba40b6f',
    '333672c7104470d9ceab827d37b6e5e355b160bcefd6ce434e9b6683b5c73c4b',
  ],
  'linux-amd64.tar.gz': [
    'bf5edc7b1ce98e885c368cb43a886eb148dc5d0551d99f3cf6141fb5e1954c70',
    'baab2eecb5a8ebdabf6edd3e22756f89805e3184468653ba67185adf5a3b57eb',
  ],
  'linux-arm64.tar.gz': [
    'e2fbab4cad7d3c76e3052eb959570393c9d73ba756b70bf8e9eea61e9ae6d49c',
    'eda00ae9bf54f28bb0ca640d80fca0e3033a722f107f0ec311791decb0ab436d',
  ],
  'darwin-amd64.tar.gz': [
    '3cba5976a4fbd022669d3401cab4147faeb2b4c079712c882edb9ae4e52feb06',
    'bdaac5b753ba5e1b6cb18c99b2648b3aa6c8f53c90a3619bc961dd6b0a3c6348',
  ],
  'darwin-arm64.tar.gz': [
    'ed0fe39222ec2e317c4d0e1a9cdc16c63ec523d94f9355d24a902a4db2655ffd',
    'b52d3bba3977dacfab306279ea2cc1e9903219598b64c04aa32b0664bdb68195',
  ],
};
const MAX_ARCHIVE = 32 * 1024 * 1024;
const MAX_BINARY = 64 * 1024 * 1024;
const run = promisify(execFile);

export function releaseFor(platform = process.platform, arch = process.arch) {
  const os = platform === 'win32' ? 'windows' : platform;
  const cpu = arch === 'x64' ? 'amd64' : arch;
  const suffix = os === 'windows' ? '.zip' : '.tar.gz';
  const key = os + '-' + cpu + suffix;
  const pins = PINS[key];
  if (!pins) throw new Error('Unsupported NATS test platform: ' + key);
  const directory = `nats-server-v${VERSION}-${os}-${cpu}`;
  const name = directory + suffix;
  const executable = os === 'windows' ? 'nats-server.exe' : 'nats-server';
  return {
    version: VERSION,
    name,
    executable,
    member: directory + '/' + executable,
    archiveHash: pins[0],
    binaryHash: pins[1],
    url: `https://github.com/nats-io/nats-server/releases/download/v${VERSION}/${name}`,
  };
}

async function checkFile(path, expected, maximum, kind) {
  const info = await stat(path);
  if (!info.isFile() || info.size === 0 || info.size > maximum)
    throw new Error(`NATS ${kind} size invalid`);
  const hash = createHash('sha256');
  let size = 0;
  for await (const chunk of createReadStream(path)) {
    size += chunk.length;
    if (size > maximum) throw new Error(`NATS ${kind} size invalid`);
    hash.update(chunk);
  }
  const actual = hash.digest('hex');
  if (actual !== expected) throw new Error(`NATS ${kind} SHA-256 mismatch`);
}

async function extractMember(archive, member, target) {
  let result;
  try {
    // Only one fixed member goes to stdout. tar never writes archive paths.
    result = await run('tar', ['-xOf', archive, member], {
      encoding: 'buffer',
      maxBuffer: MAX_BINARY,
      timeout: 30000,
      windowsHide: true,
    });
  } catch {
    throw new Error('NATS fixed archive member extraction failed');
  }
  await writeFile(target, result.stdout, { flag: 'wx' });
}

/** Test-only preparation shared by the two required WSS tests. A fresh private
 * stage per process/promise never exposes a partially downloaded executable.
 * Injected fetch/stage/extraction are for negative tests; release pins stay fixed. */
export function createNatsBinaryPreparer({
  find = findNatsServer,
  stageRoot = join(tmpdir(), 'auriga-nats-downloads'),
  fetchImpl = globalThis.fetch,
  extract = extractMember,
} = {}) {
  let preparing;
  return async function ensure() {
    const ready = await (preparing ??= (async () => {
      const release = releaseFor();
      const installed = find();
      if (installed) {
        await checkFile(installed, release.binaryHash, MAX_BINARY, 'binary');
        return installed;
      }
      await mkdir(stageRoot, { recursive: true });
      const stage = await mkdtemp(join(stageRoot, `nats-${process.pid}-`));
      try {
        const archive = join(stage, release.name);
        const response = await fetchImpl(release.url, {
          signal: AbortSignal.timeout(45000),
          redirect: 'follow',
        });
        if (
          !response.ok ||
          Number(response.headers.get('content-length')) > MAX_ARCHIVE
        ) {
          await response.body?.cancel();
          throw new Error(
            !response.ok
              ? `NATS download HTTP ${response.status}`
              : 'NATS archive too large',
          );
        }
        if (!response.body)
          throw new Error('NATS archive response body missing');
        const reader = response.body.getReader(),
          file = await open(archive, 'wx');
        let size = 0;
        try {
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            size += value.byteLength;
            if (size > MAX_ARCHIVE) {
              await reader.cancel();
              throw new Error('NATS archive too large');
            }
            await file.writeFile(value);
          }
        } finally {
          await file.close();
          reader.releaseLock();
        }
        await checkFile(archive, release.archiveHash, MAX_ARCHIVE, 'archive');
        const pending = join(stage, 'binary.part');
        await extract(archive, release.member, pending);
        await checkFile(pending, release.binaryHash, MAX_BINARY, 'binary');
        if (process.platform !== 'win32') await chmod(pending, 0o700);
        const ready = join(stage, release.executable);
        await rename(pending, ready);
        return ready;
      } catch (error) {
        await writeFile(
          join(stage, 'failure.txt'),
          String(error.message),
        ).catch(() => {});
        throw error;
      }
    })());
    await checkFile(ready, releaseFor().binaryHash, MAX_BINARY, 'binary');
    return ready;
  };
}

export const ensureNatsServer = createNatsBinaryPreparer();
