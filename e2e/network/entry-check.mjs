import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join, resolve, delimiter } from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { javaClasspath } from './support/assembly-process.mjs';
import { dualProxyHeaders } from './support/dual-proxy.mjs';

assert.equal(process.argv.slice(2).join(' '), '--run');
const root = process.cwd();
const target = resolve('e2e/network/target');
await mkdir(target, { recursive: true });
const output = await mkdtemp(join(target, 'entry-check-'));
const accessFile = join(root, 'contracts/deployment/fixtures/ip-http.json');
const access = JSON.parse(await readFile(accessFile, 'utf8'));
const { scheme, host, port } = access.portalOrigin;
const origin = `${scheme}://${host}:${port}`;
const after = dualProxyHeaders(
  {
    origin,
    'X-Auriga-Client-IP': '192.0.2.99',
    'X-Auriga-Entry-Role': 'admin',
  },
  origin,
  '127.0.0.1',
  true,
);
const before = { ...after };
delete before['x-auriga-client-ip'];
const requests = join(output, 'headers.json');
await writeFile(
  requests,
  JSON.stringify(
    {
      before,
      after,
      foreignOrigin: { ...after, origin: 'http://other.example.com' },
    },
    null,
    2,
  ),
);
const classpath = await javaClasspath(root);
const run = promisify(execFile);
const binary = (name) =>
  join(
    process.env.JAVA_HOME,
    'bin',
    name + (process.platform === 'win32' ? '.exe' : ''),
  );
const compiled = await run(
  binary('javac'),
  [
    '-cp',
    classpath,
    '-d',
    output,
    join(root, 'e2e/network/support/DualDshEntryCheck.java'),
  ],
  { cwd: root, windowsHide: true, timeout: 30000 },
);
await writeFile(join(output, 'javac.log'), compiled.stdout + compiled.stderr);
const checked = await run(
  binary('java'),
  [
    '-cp',
    output + delimiter + classpath,
    'auriga.iam.DualDshEntryCheck',
    accessFile,
    requests,
  ],
  { cwd: root, windowsHide: true, timeout: 30000 },
);
await writeFile(join(output, 'java.log'), checked.stdout + checked.stderr);
const cases = checked.stdout
  .split(/\r?\n/)
  .filter((line) => line.startsWith('{'))
  .map((line) => JSON.parse(line));
assert.deepEqual(cases, [
  { case: 'before', status: 400, code: 'common.validation_failed' },
  { case: 'after', ingressAccepted: true, clientIp: '127.0.0.1' },
  { case: 'foreignOrigin', status: 403, code: 'common.forbidden' },
]);
const result = {
  output,
  cases,
  scope: 'production RequestIdentity only; no IAM login, PG or SEND',
  exit: 0,
};
await writeFile(join(output, 'manifest.json'), JSON.stringify(result, null, 2));
console.log(JSON.stringify(result));
