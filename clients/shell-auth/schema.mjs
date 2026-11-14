import { createRequire } from 'node:module';
import { existsSync, readFileSync } from 'node:fs';
const packaged = existsSync(
  new URL('./schemas/plugin/shell-auth.schema.json', import.meta.url),
);
const base = packaged
  ? new URL('./schemas/', import.meta.url)
  : new URL('../../contracts/', import.meta.url);
const loader = packaged
  ? createRequire(new URL('./package.json', import.meta.url))
  : createRequire(new URL('../dsh-plugin/package.json', import.meta.url));
const Ajv = loader(packaged ? './vendor-ajv.cjs' : 'ajv/dist/2020.js').default;
const ajv = new Ajv({ strict: false, allErrors: true });
ajv.addFormat(
  'uuid',
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
);
ajv.addFormat(
  'date-time',
  (s) => /^\d{4}-\d\d-\d\dT/.test(s) && Number.isFinite(Date.parse(s)),
);
ajv.addFormat('uri', (s) => {
  try {
    return Boolean(new URL(s).protocol);
  } catch {
    return false;
  }
});
ajv.addFormat('int64', { type: 'number', validate: Number.isSafeInteger });
ajv.addSchema(
  JSON.parse(
    readFileSync(new URL('network/register-result.schema.json', base), 'utf8'),
  ),
);
const files = [
  'auth/uac-login-info',
  'auth/uac-session-binding',
  'auth/uac-control',
  'auth/standard-device',
  'plugin/shell-auth',
  'plugin/shell-auth-control',
  'plugin/shell-business',
];
const definitions = new Map();
for (const file of files) {
  const url = new URL(file + '.schema.json', base),
    schema = JSON.parse(readFileSync(url, 'utf8'));
  // Keep schema identities independent of installation paths (including the
  // RUNNER~1 short paths used by Windows CI and ordinary user installations).
  const id = new URL(
    file + '.schema.json',
    'https://auriga.example.com/contracts/',
  ).href;
  ajv.addSchema(schema, id);
  for (const name of Object.keys(schema.$defs))
    definitions.set(name, `${id}#/$defs/${name}`);
}
export const valid = (name, value) =>
  Boolean(definitions.has(name) && ajv.getSchema(definitions.get(name))(value));
export function requireValid(name, value) {
  if (!valid(name, value))
    throw Object.assign(new Error('common.validation_failed'), {
      code: 'common.validation_failed',
    });
  return value;
}
export function contractFile(path) {
  return readFileSync(new URL(path, base), 'utf8');
}
