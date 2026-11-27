import { constants } from 'node:fs';
import { lstat, open, opendir, realpath } from 'node:fs/promises';
import { join, relative, resolve } from 'node:path';

/** @param {string} path @param {string} reason @returns {never} */
export function invalid(path, reason) {
  throw Object.assign(new Error(path + ': ' + reason), {
    code: 'common.validation_failed',
    path,
    reason,
  });
}

/** @param {string} path */
export function safePath(path) {
  return path
    .split('/')
    .every(
      (part) =>
        part &&
        part !== '.' &&
        part !== '..' &&
        !/[. ]$/.test(part) &&
        !/[\\:<>"|?*]/.test(part) &&
        ![...part].some(
          (c) => c.charCodeAt(0) < 32 || c.charCodeAt(0) === 127,
        ) &&
        !/^(?:con|prn|aux|nul|conin\$|conout\$|(?:com|lpt)[1-9¹²³])(?:\.|$)/i.test(
          part,
        ),
    );
}

/** @param {string} name */
export function excluded(name) {
  return (
    name.startsWith('.') ||
    /^(?:node_modules|coverage|tests?|__tests__|fixtures|secrets?|credentials|private[-_]?keys?|Dockerfile|Makefile|(?:docker-)?compose(?:\..*)?\.(?:yaml|yml)|(?:package-lock|npm-shrinkwrap)\.json|(?:pnpm-lock|pnpm-workspace)\.ya?ml|yarn\.lock|bun\.lockb?)$/i.test(
      name,
    ) ||
    /\.(?:pem|key|p12|pfx|jks|keystore|crt|cer|kdbx|sqlite3?|db|log|bak|tgz|zip)$/i.test(
      name,
    ) ||
    /^(?:env|secrets?|credentials|tokens?|private[-_]?keys?|id_(?:rsa|ed25519|ecdsa))(?:\.|$)/i.test(
      name,
    ) ||
    /^(?:config|settings|application|appsettings)\.(?:(?:local|dev|development|prod|production|test)\.)?(?:json|toml|ini|ya?ml|properties)$/i.test(
      name,
    ) ||
    /^(?:tsconfig|jsconfig|eslint|prettier|vite|vitest|webpack|jest|rollup|babel|postcss|tailwind)(?:\.|-|$)/i.test(
      name,
    )
  );
}

/** @param {string} cwd @param {string} directory */
export async function openSubmissionDirectory(cwd, directory) {
  const source =
    typeof directory === 'string' ? directory.replaceAll('\\', '/') : '';
  if (source !== '.' && (!safePath(source) || source.split('/').length > 64))
    invalid('.', 'directory must be a safe relative path');
  try {
    const session = await realpath(resolve(cwd));
    const root = source === '.' ? session : await checkedPath(session, source);
    if (!(await lstat(root)).isDirectory())
      invalid(source, 'expected a directory');
    return root;
  } catch (error) {
    return filesystemError(source, error);
  }
}

/** @param {string} path @param {unknown} error @returns {never} */
function filesystemError(path, error) {
  if (error instanceof Error && 'reason' in error) throw error;
  invalid(path, 'cannot read local path or directory');
}

/** @param {string} root @param {string} path @param {boolean} [optional] */
async function checkedPath(root, path, optional = false) {
  let current = root;
  for (const part of path.split('/')) {
    current = join(current, part);
    const info = await lstat(current).catch((error) => {
      if (error.code !== 'ENOENT') throw error;
      if (!optional) invalid(path, 'missing local file or directory');
      return null;
    });
    if (!info) return '';
    if (info.isSymbolicLink())
      invalid(path, 'symbolic links and junctions are forbidden');
  }
  if (relative(current, await realpath(current)))
    invalid(path, 'resolved path differs from selected path');
  return current;
}

/** @param {string} root */
export function submissionFiles(root) {
  let visited = 0;
  return {
    /** @param {string} path @param {boolean} [optional] */
    async stat(path, optional = false) {
      if (++visited > 32768 || !safePath(path))
        invalid(path, 'path or directory scan limit exceeded');
      try {
        const full = await checkedPath(root, path, optional);
        if (!full) return null;
        const info = await lstat(full, { bigint: true });
        if (!info.isDirectory() && (!info.isFile() || info.nlink !== 1n))
          invalid(path, 'only regular files without hard links are allowed');
        return info;
      } catch (error) {
        return filesystemError(path, error);
      }
    },
    /** @param {string} path */
    async *children(path) {
      try {
        const full = path ? await checkedPath(root, path) : root;
        const dir = await opendir(full);
        for await (const entry of dir) {
          if (++visited > 32768)
            invalid(path || '.', 'directory scan limit exceeded');
          yield entry.name;
        }
      } catch (error) {
        return filesystemError(path || '.', error);
      }
    },
    /** @param {string} path @param {import('node:fs').BigIntStats} info @param {number} maxBytes */
    async read(path, info, maxBytes) {
      if (!info.isFile()) invalid(path, 'expected a regular file');
      if (info.size > BigInt(maxBytes))
        invalid(path, 'file or archive size limit exceeded');
      /** @param {import('node:fs').BigIntStats} actual */
      const unchanged = (actual) =>
        actual.dev === info.dev &&
        actual.ino === info.ino &&
        actual.nlink === 1n &&
        actual.isFile() &&
        actual.size === info.size &&
        actual.mtimeNs === info.mtimeNs &&
        actual.ctimeNs === info.ctimeNs;
      try {
        const full = await checkedPath(root, path);
        const flags =
          constants.O_RDONLY |
          (constants.O_NOFOLLOW ?? 0) |
          (constants.O_NONBLOCK ?? 0);
        const handle = await open(full, flags);
        try {
          if (!unchanged(await handle.stat({ bigint: true })))
            invalid(path, 'file changed during packaging');
          const bytes = Buffer.alloc(Number(info.size));
          let offset = 0;
          while (offset < bytes.length) {
            const { bytesRead } = await handle.read(
              bytes,
              offset,
              bytes.length - offset,
              offset,
            );
            if (!bytesRead) invalid(path, 'file changed during packaging');
            offset += bytesRead;
          }
          if (
            !unchanged(await handle.stat({ bigint: true })) ||
            !unchanged(
              await lstat(await checkedPath(root, path), { bigint: true }),
            )
          )
            invalid(path, 'file changed during packaging');
          return bytes;
        } finally {
          await handle.close();
        }
      } catch (error) {
        return filesystemError(path, error);
      }
    },
  };
}
