import { createRequire } from 'node:module';
import { existsSync, readFileSync } from 'node:fs';

let validate;
export function configureSchema(Ajv, base) {
  const ajv = new Ajv({ strict: true, allErrors: true });
  ajv.addFormat(
    'uuid',
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
  );
  ajv.addFormat('int64', { type: 'number', validate: Number.isSafeInteger });
  ajv.addFormat('uri', (s) => {
    try {
      return Boolean(new URL(s).protocol);
    } catch {
      return false;
    }
  });
  ajv.addFormat(
    'date-time',
    (s) =>
      /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(s) &&
      Number.isFinite(Date.parse(s)),
  );
  for (const name of ['signatures', 'package', 'unified']) {
    const url = new URL(`${name}.schema.json`, base);
    ajv.addSchema(JSON.parse(readFileSync(url, 'utf8')), url.href);
  }
  validate = (file, name, value) =>
    Boolean(
      ajv.getSchema(
        new URL(`${file}.schema.json`, base).href + '#/$defs/' + name,
      )(value),
    );
}

export function schemaValid(file, name, value) {
  if (!validate) {
    const shipped = new URL('./schemas/', import.meta.url);
    const packaged = existsSync(new URL('unified.schema.json', shipped));
    const loader = packaged
      ? createRequire(
          new URL('../../upstream/app/package.json', import.meta.url),
        )
      : createRequire(
          new URL('../../dsh-plugin/package.json', import.meta.url),
        );
    const Ajv = loader('ajv/dist/2020.js');
    configureSchema(
      Ajv.default ?? Ajv,
      packaged
        ? shipped
        : new URL('../../../contracts/resource/', import.meta.url),
    );
  }
  return validate(file, name, value);
}

export function requireSchema(name, value) {
  if (!schemaValid('unified', name, value)) throw new Error(`invalid ${name}`);
  return value;
}
