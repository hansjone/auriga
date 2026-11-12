import test from 'node:test';
import assert from 'node:assert/strict';
import { join, delimiter } from 'node:path';
import { spawnSync } from 'node:child_process';
import { writeFileSync, mkdirSync } from 'node:fs';
import { systemTool } from '../runtime/system-tools.mjs';
import { tempBase } from './helpers.mjs';
import { extractArchive } from '../runtime/update.mjs';

test(
  'Windows System32 tools ignore Git usr/bin on PATH',
  { skip: process.platform !== 'win32' },
  (t) => {
    const base = tempBase(t),
      fake = join(base, 'Git', 'usr', 'bin');
    mkdirSync(fake, { recursive: true });
    for (const name of ['tar', 'whoami', 'icacls'])
      writeFileSync(join(fake, name + '.cmd'), '@exit /b 99\r\n');
    const pathKey = Object.keys(process.env).find(
      (key) => key.toLowerCase() === 'path',
    );
    const previous = process.env[pathKey];
    process.env[pathKey] = [
      fake,
      'C:\\Program Files\\Git\\usr\\bin',
      previous,
    ].join(delimiter);
    try {
      for (const name of ['tar', 'whoami', 'icacls'])
        assert.equal(
          systemTool(name),
          join(process.env.SystemRoot, 'System32', name + '.exe'),
        );
      assert.equal(
        spawnSync(systemTool('whoami'), ['/user', '/fo', 'csv', '/nh']).status,
        0,
      );
      assert.equal(spawnSync(systemTool('icacls'), [base]).status, 0);
      const stage = join(base, 'stage');
      mkdirSync(stage);
      mkdirSync(join(stage, 'payload'));
      writeFileSync(join(stage, 'payload', 'ok'), 'yes');
      const archive = join(base, 'test.zip');
      assert.equal(
        spawnSync(systemTool('tar'), ['-a', '-cf', archive, '-C', stage, '.'])
          .status,
        0,
      );
      assert.equal(
        extractArchive(archive, join(base, 'expanded')),
        join(base, 'expanded', 'payload'),
      );
    } finally {
      process.env[pathKey] = previous;
    }
  },
);
