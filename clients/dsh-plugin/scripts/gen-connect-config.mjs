/* global structuredClone */
import { readFile, writeFile } from 'node:fs/promises';
import { compile } from 'json-schema-to-typescript';
import { format } from 'prettier';

const directory = new URL('../../../contracts/plugin/', import.meta.url);
const source = JSON.parse(
  await readFile(new URL('local-login.schema.json', directory), 'utf8'),
);
const address = JSON.parse(
  await readFile(new URL('../network/address.schema.json', directory), 'utf8'),
);
// Project only the referenced address scalars: its document root is Address,
// which json-schema-to-typescript 16 otherwise follows instead of the fragment.
const network = structuredClone(source.$defs.ConnectNetwork);
network.properties.realm = { $ref: '#/$defs/PositiveUint64' };
const schema = {
  ...source.$defs.ConnectConfig,
  title: 'ConnectConfig',
  $defs: {
    ConnectNetwork: network,
    NetworkRootPin: source.$defs.NetworkRootPin,
    PositiveUint64: address.$defs.PositiveUint64,
    Uint64: address.$defs.Uint64,
  },
};
const style = JSON.parse(
  await readFile(new URL('../../../.prettierrc.json', import.meta.url), 'utf8'),
);
await writeFile(
  new URL('../src/generated/connect-config.d.ts', import.meta.url),
  await format(
    await compile(schema, 'ConnectConfig', { ignoreMinAndMaxItems: true }),
    { ...style, parser: 'typescript' },
  ),
);
