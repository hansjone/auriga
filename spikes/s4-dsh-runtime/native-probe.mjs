// Run using the artifact's Node; resolve every dependency from the artifact, not this checkout.
import { createRequire } from 'node:module';
import { openSync, closeSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { options } from './lib.mjs';
const args = options(process.argv.slice(2), ['anchor', 'tmp']);
if (!args.anchor || !args.tmp) throw new Error('--anchor and --tmp are required');
const require = createRequire(resolve(args.anchor));
const result = {};
require('koffi'); result.koffi = 'loaded';
const sharp = require('sharp');
const png = await sharp({ create: { width: 1, height: 1, channels: 4, background: '#000000' } }).png().toBuffer();
if (!png.length) throw new Error('sharp produced no image');
result.sharp = 'rendered-1px-png';
if (process.platform === 'linux') {
  const flock = await import(require.resolve('@deepseek-ai/node-addon-system/flock'));
  const fd = openSync(join(args.tmp, 'flock-probe'), 'w');
  try { await flock.tryLockExclusive(fd); } finally { closeSync(fd); }
  result.flock = 'locked';
} else result.flock = 'unsupported-on-windows';
const pty = require('node-pty');
let terminal;
try {
  result.pty = await new Promise((resolveProbe, reject) => {
    const timer = setTimeout(() => reject(new Error('native PTY timed out')), 10000);
    const windows = process.platform === 'win32';
    terminal = pty.spawn(windows ? process.env.ComSpec ?? 'C:\\Windows\\System32\\cmd.exe' : '/bin/sh', windows ? ['/d', '/s', '/c', 'echo s4-native-ok'] : ['-c', 'printf s4-native-ok'], { name: 'xterm', cols: 80, rows: 24, cwd: args.tmp, env: process.env });
    let output = '';
    terminal.onData(data => { output += data; });
    terminal.onExit(({ exitCode }) => {
      clearTimeout(timer);
      if (exitCode === 0 && output.includes('s4-native-ok')) resolveProbe('spawned-and-exited');
      else reject(new Error(`native PTY exit=${exitCode}, expected output missing=${!output.includes('s4-native-ok')}`));
    });
  });
} finally { try { terminal?.kill(); } catch { /* Already exited. */ } }
console.log(JSON.stringify(result));
