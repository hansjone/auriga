import fs from 'node:fs';
import path from 'node:path';
import { platformConfig } from '../../clients/dsh-plugin/src/local/platform.js';
import { parseStrictJson } from '../../clients/dsh-plugin/src/network/trust/jcs.js';

// Public installation metadata only. No remote discovery of the initial root.
export function validateConnect(input) {
  try {
    const value = platformConfig(input);
    if (
      !value.network ||
      value.platformOrigin !== input.platformOrigin ||
      new URL(value.platformOrigin).protocol !== 'https:'
    )
      throw new Error();
    return value;
  } catch {
    throw new Error(
      'blocked：需要受信 HTTPS connect 配置及规范 network 静态公钥 pin',
    );
  }
}

export function regularFile(file, maxBytes) {
  if (
    !path.isAbsolute(file) ||
    file.startsWith('\\\\') ||
    file.startsWith('//')
  )
    throw new Error('需要本机绝对文件路径');
  if (process.platform === 'win32' && file.slice(2).includes(':'))
    throw new Error('文件路径不接受设备流');
  for (let current = file; ; current = path.dirname(current)) {
    const stat = fs.lstatSync(current);
    if (
      stat.isSymbolicLink() ||
      path.resolve(fs.realpathSync(current)).toLowerCase() !==
        current.toLowerCase()
    )
      throw new Error('文件路径不能包含链接');
    if (current === file && (!stat.isFile() || stat.size > maxBytes))
      throw new Error('文件类型或大小不符合要求');
    if (path.dirname(current) === current) break;
  }
  return fs.readFileSync(file);
}

export function readConnect(file) {
  try {
    return validateConnect(parseStrictJson(regularFile(file, 65536)));
  } catch {
    // Never echo parser input, URLs containing credentials, or untrusted errors.
    throw new Error(
      'blocked：公开 connect 文件无效；请核路径、严格 JSON、HTTPS 和 network pin',
    );
  }
}
