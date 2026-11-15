import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
export function releaseVersion(ref, pluginVersion, eventName = '') {
  const official =
    ref.startsWith('desktop-v') ||
    ref.startsWith('plugin-v') ||
    eventName === 'workflow_dispatch';
  const version = ref.startsWith('plugin-v')
    ? ref.slice('plugin-v'.length)
    : official
      ? pluginVersion
      : '0.0.0-ci';
  if (official && version === '0.0.0-ci')
    throw new Error('CI placeholder forbidden in official release');
  return version;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const plugin = JSON.parse(
    readFileSync(
      new URL('../../clients/dsh-plugin/package.json', import.meta.url),
    ),
  );
  console.log(
    releaseVersion(
      process.argv[2] ?? '',
      plugin.version,
      process.env.GITHUB_EVENT_NAME,
    ),
  );
}
