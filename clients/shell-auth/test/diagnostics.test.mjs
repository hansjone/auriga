import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readdirSync,
  rmSync,
  symlinkSync,
} from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { randomUUID } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { exportDiagnostics, redact } from '../diagnostics.mjs';
import { buildHelper } from '../windows/build.mjs';

test('recursive secret and personal data are absent from positive diagnostic projection', () => {
  const secret = randomUUID(),
    person = randomUUID();
  const value = redact({
    correlationId: randomUUID(),
    token: secret,
    headers: { Authorization: secret },
    unknown: { list: [person] },
    name: person,
    department: person,
    email: person,
    message: person,
    udsMessage: secret,
    result: 'failure',
    purpose: 'login_info',
    empNo: '001',
  });
  assert.ok(!JSON.stringify(value).includes(secret));
  assert.ok(!JSON.stringify(value).includes(person));
  assert.equal(value.departmentPresent, true);
  assert.equal(value.udsMessage, '[REDACTED]');
  assert.equal(value.empNo, '001');
});
test(
  'installed export creates independently readable ZIP, checks hashes and reports missing sources',
  { skip: process.platform !== 'win32' },
  async (t) => {
    const dir = mkdtempSync(join(tmpdir(), 'auriga-diag-'));
    t.after(() => rmSync(dir, { recursive: true, force: true }));
    const root = join(dir, 'install'),
      dataHome = join(dir, 'data'),
      output = join(dir, 'export');
    mkdirSync(root);
    mkdirSync(join(dataHome, 'auriga', 'logs'), { recursive: true });
    writeFileSync(join(root, '.auriga-desktop-install'), 'test');
    writeFileSync(
      join(root, 'desktop-origin.json'),
      JSON.stringify({
        origin: 'https://example.com',
        allowRawAgentToken: false,
      }),
    );
    writeFileSync(
      join(root, 'install.json'),
      JSON.stringify({ current: 'test-build', dataHome }),
    );
    const helper = buildHelper(join(dir, 'helper.exe')),
      tenantId = randomUUID(),
      providerId = randomUUID(),
      secret = randomUUID();
    const record = {
      timestamp: new Date().toISOString(),
      tenantId,
      providerId,
      correlationId: randomUUID(),
      stage: 'department_lookup',
      purpose: 'login_info',
      result: 'failure',
      token: secret,
      name: secret,
      department: secret,
    };
    writeFileSync(
      join(dataHome, 'auriga', 'logs', 'auriga-shell-auth.jsonl'),
      JSON.stringify(record) +
        '\nmalformed\n' +
        JSON.stringify({
          ...record,
          tenantId: randomUUID(),
          empNo: 'wrong-tenant',
        }) +
        '\n',
    );
    const result = await exportDiagnostics({
      root,
      output,
      helper,
      tenantId,
      providerId,
    });
    assert.equal(result.partial, true);
    assert.equal(result.dropped, 1);
    assert.ok(result.missingSources.includes('loki'));
    assert.ok(!readdirSync(output).some((n) => n.startsWith('.uac-')));
    const check = execFileSync(
      'python',
      [
        '-c',
        `import zipfile,json,hashlib,sys\nz=zipfile.ZipFile(sys.argv[1]);assert z.testzip() is None\nm=json.loads(z.read('manifest.json'));assert m['partial']\nfor f in m['files']: assert hashlib.sha256(z.read(f['name'])).hexdigest()==f['sha256']\ns=z.read('uac-steps.jsonl').decode();assert sys.argv[2] not in s;assert 'wrong-tenant' not in s\nc=json.loads(z.read('sanitized-config.json'));assert c['installationConfiguration']['allowRawAgentToken'] is False;assert c['uacEffectiveConfigAvailable'] is False\nprint('zip/hash/redaction OK')`,
        result.file,
        secret,
      ],
      { encoding: 'utf8' },
    );
    assert.match(check, /OK/);
    await assert.rejects(
      exportDiagnostics({
        root,
        output,
        helper,
        tenantId,
        providerId,
        from: '2020-01-01T00:00:00Z',
        to: '2020-01-03T00:00:00Z',
      }),
      /time window/,
    );
    const alias = join(dir, 'alias');
    symlinkSync(output, alias, 'junction');
    await assert.rejects(
      exportDiagnostics({ root, output: alias, helper, tenantId, providerId }),
      /reparse/,
    );
  },
);
