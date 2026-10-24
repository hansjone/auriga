// @ts-nocheck
import { createRequire } from 'node:module';
import { readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { pathToFileURL } from 'node:url';

/** Resolve only the already-inventoried package against native profile/installation roots. Never execute it. */
export function installedPackage(host, name) {
  if (
    !/^(?:@[a-z0-9._-]+\/)?[a-z0-9][a-z0-9._-]*$/i.test(name) ||
    name.length > 214
  )
    return undefined;
  const profile = host.profileContext;
  for (const anchor of [
    profile?.dir && join(profile.dir, 'package.json'),
    profile?.installAnchor,
  ]) {
    if (typeof anchor !== 'string') continue;
    try {
      const file = createRequire(pathToFileURL(anchor)).resolve(name);
      if (!statSync(file).isFile()) continue;
      let directory = dirname(file);
      for (let depth = 0; depth < 12; depth++) {
        try {
          const pkg = JSON.parse(
            readFileSync(join(directory, 'package.json'), 'utf8'),
          );
          if (pkg.name === name)
            return {
              version:
                typeof pkg.version === 'string' &&
                /^[0-9A-Za-z.+_-]{1,128}$/.test(pkg.version)
                  ? pkg.version
                  : undefined,
            };
        } catch {
          /* Only walk the resolved package's ancestor manifests. */
        }
        const parent = dirname(directory);
        if (parent === directory) break;
        directory = parent;
      }
      return {};
    } catch {
      /* Missing or unsupported resolution is unknown, never an invented install. */
    }
  }
  return undefined;
}
