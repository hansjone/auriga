import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import nodeTest from 'node:test';
export const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (!root && process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1')
  throw new Error(
    'dedicated tests require the locked rc.3 public package root',
  );
const require = root ? createRequire(join(root, 'package.json')) : undefined;
export const test = (name, body) =>
  nodeTest(
    name,
    { skip: !root && 'locked rc.3 public package root is not configured' },
    body,
  );
export const load = (name) =>
  import(pathToFileURL(require.resolve('@deepseek-ai/' + name)).href);
