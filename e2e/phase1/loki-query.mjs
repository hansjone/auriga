import { spawn } from 'node:child_process';
import { Buffer } from 'node:buffer';

// The operator-owned command is an argv array, never a shell expression. Its
// stdin is one query URL; stdout must contain only the original Loki JSON.
export async function queryLoki(command, url, timeout = 15000) {
  if (!Number.isSafeInteger(timeout) || timeout < 1 || timeout > 60000)
    throw new Error('Loki queryTimeoutMs须为1到60000的整数');
  if (
    !Array.isArray(command) ||
    !command.length ||
    command.length > 64 ||
    !command[0] ||
    command.some(
      (arg) =>
        typeof arg !== 'string' || arg.includes('\0') || arg.length > 8192,
    )
  )
    throw new Error('Loki queryCommand必须是受信任的命令参数数组');
  return new Promise((resolve, reject) => {
    const child = spawn(command[0], command.slice(1), {
      shell: false,
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'ignore'],
      signal: globalThis.AbortSignal.timeout(timeout),
    });
    const chunks = [];
    let size = 0;
    const fail = () => reject(new Error('Loki远程查询失败或返回无效JSON'));
    child.on('error', fail);
    child.stdin.on('error', fail);
    child.stdout.on('data', (chunk) => {
      size += chunk.length;
      if (size > 1048576) {
        child.kill();
        fail();
      } else chunks.push(chunk);
    });
    child.on('close', (code) => {
      if (code !== 0 || size > 1048576) return fail();
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
      } catch {
        fail();
      }
    });
    child.stdin.end(url.href + '\n');
  });
}
