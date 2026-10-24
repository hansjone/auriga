/** Adapted from dsh-team/lib/utils/atomic-write.js (MIT; ../SOURCES.md). */
import { open, rename, mkdir, unlink } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { randomBytes } from 'node:crypto';

/** @param {string} file @param {unknown} value */
export async function atomicWriteJson(file, value) {
  await mkdir(dirname(file), { recursive: true });
  const temporary = join(
    dirname(file),
    `.${randomBytes(16).toString('hex')}.tmp`,
  );
  let handle;
  try {
    handle = await open(temporary, 'wx', 0o600);
    await handle.writeFile(JSON.stringify(value) + '\n', 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, file);
  } finally {
    await handle?.close();
    await unlink(temporary).catch((error) => {
      if (error.code !== 'ENOENT') throw error;
    });
  }
}
