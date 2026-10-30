import test from 'node:test';
import assert from 'node:assert/strict';
import { createTestLookup } from './cloud-desk.dns.mjs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

test('进程内域名映射兼容Node单地址和all查询，未知名字继续正常解析', async () => {
  const delegated = [];
  const lookup = createTestLookup(
    { 'portal.example.com': '127.0.0.1' },
    (name, options, callback) => {
      delegated.push(name);
      callback(null, '192.0.2.1', 4);
    },
  );
  const one = await new Promise((resolve) =>
    lookup('portal.example.com', {}, (...result) => resolve(result)),
  );
  assert.deepEqual(one, [null, '127.0.0.1', 4]);
  const all = await new Promise((resolve) =>
    lookup('portal.example.com', { all: true }, (...result) => resolve(result)),
  );
  assert.deepEqual(all, [null, [{ address: '127.0.0.1', family: 4 }]]);
  await new Promise((resolve) =>
    lookup('other.example.com', {}, () => resolve()),
  );
  assert.deepEqual(delegated, ['other.example.com']);
  assert.throws(() =>
    createTestLookup({ 'portal.example.com': '203.0.113.1' }, () => {}),
  );
});

test('测试DNS预载模块以文件URL启动，影响仅限显式配置的子进程', async () => {
  const run = promisify(execFile);
  const { stdout } = await run(
    process.execPath,
    [
      '--import',
      new URL('./cloud-desk.dns.mjs', import.meta.url).href,
      '--input-type=module',
      '-e',
      "import {lookup} from 'node:dns';lookup('portal.example.com',{all:true},(error,rows)=>{if(error)process.exitCode=1;else console.log(JSON.stringify(rows))});",
    ],
    {
      env: {
        ...process.env,
        AURIGA_CLOUD_DESK_DNS: '{"portal.example.com":"127.0.0.1"}',
      },
      windowsHide: true,
      timeout: 10000,
    },
  );
  assert.deepEqual(JSON.parse(stdout), [{ address: '127.0.0.1', family: 4 }]);
});
