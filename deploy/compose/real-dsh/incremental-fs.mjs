// Incremental builds retain the original manifest generator byte-for-byte.
// Its executable normalization must not copy up unchanged lower-layer binaries.
import fs from 'node:fs';
import { syncBuiltinESMExports } from 'node:module';

const chmod = fs.chmodSync;
fs.chmodSync = (path, mode) => {
  if (mode === 0o755) {
    try {
      const stat = fs.lstatSync(path);
      if (stat.isFile() && (stat.mode & 0o7777) === mode) return;
    } catch {
      // Preserve the original chmod syscall and its error if probing fails.
    }
  }
  return chmod(path, mode);
};
syncBuiltinESMExports();
