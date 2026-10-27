import { spawn } from 'node:child_process';
import { access, readFile, stat } from 'node:fs/promises';
import { join, resolve, delimiter } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

/** One owned child; stdout is a public JSON stage channel, stderr stays private
 * until the supervisor has checked it for credential leakage. No shell. */
export function childProcess(executable, args, options = {}) {
  const child = spawn(executable, args, {
    ...options,
    windowsHide: true,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  const lines = [],
    errors = [];
  const publicEvents = [];
  let partial = '',
    failure,
    exit;
  const done = new Promise((resolve) => {
    child.once('error', (error) => {
      failure = error;
    });
    child.once('close', (code, signal) => {
      exit = { code, signal };
      resolve(exit);
    });
  });
  child.stdin.on('error', (error) => {
    failure = error;
  });
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (part) => {
    errors.push(part);
  });
  child.stdout.on('data', (part) => {
    partial += part;
    if (partial.length > 4 * 1024 * 1024) {
      failure = new Error('child output line exceeds bound');
      child.stdin.end();
      return;
    }
    for (;;) {
      const end = partial.indexOf('\n');
      if (end < 0) break;
      const line = partial.slice(0, end).replace(/\r$/, '');
      partial = partial.slice(end + 1);
      lines.push(line);
      if (line.startsWith('{')) {
        try {
          publicEvents.push(JSON.parse(line));
        } catch {
          /* Production logs. */
        }
      }
    }
  });
  let id = 0;
  const api = {
    child,
    done,
    lines,
    errors,
    events: publicEvents,
    async wait(predicate, name, timeout = 180_000) {
      const until = Date.now() + timeout;
      for (;;) {
        const event = publicEvents.find(predicate);
        if (event) return event;
        if (failure) throw new Error(`${name}: child launch/output failed`);
        if (exit)
          throw new Error(`${name}: child exited ${exit.code}/${exit.signal}`);
        if (Date.now() >= until) throw new Error(`${name}: deadline exceeded`);
        await delay(20);
      }
    },
    write(value) {
      if (exit || child.stdin.destroyed)
        throw new Error('owned child is closed');
      child.stdin.write(
        typeof value === 'string' ? value + '\n' : JSON.stringify(value) + '\n',
      );
    },
    async request(command, timeout = 30_000) {
      const requestId = String(++id);
      api.write({ ...command, id: requestId });
      return api.wait(
        (v) => v.stage === 'reply' && v.id === requestId,
        command.op,
        timeout,
      );
    },
    async stop(command = 'stop') {
      if (!exit) {
        try {
          api.write(command);
        } finally {
          child.stdin.end();
        }
      }
      const stopped = await Promise.race([
        done,
        delay(30_000, null, { ref: false }),
      ]);
      if (!stopped) {
        // Only the exact child handle created above, never an ambient PID.
        child.kill();
        await Promise.race([done, delay(10_000, null, { ref: false })]);
        throw new Error('owned child required forced shutdown');
      }
      if (stopped.code !== 0)
        throw new Error(`owned child exited ${stopped.code}/${stopped.signal}`);
      return stopped;
    },
  };
  return api;
}

const unescapeXml = (value) =>
  value
    .replace(/&quot;/g, '"')
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>');

export async function javaClasspath(root) {
  const report = join(
    root,
    'services/platform-app/target/surefire-reports/TEST-auriga.iam.NetworkSourceJavaHostTest.xml',
  );
  const xml = await readFile(report, 'utf8');
  const suite = xml.match(/<testsuite\b[^>]*>/)?.[0];
  if (
    !suite ||
    !/failures="0"/.test(suite) ||
    !/errors="0"/.test(suite) ||
    !/skipped="0"/.test(suite) ||
    Number(/tests="(\d+)"/.exec(suite)?.[1]) < 5
  )
    throw new Error('fresh five-case non-PG bridge test is required');
  const property = xml.match(
    /<property name="surefire.test.class.path" value="([^"]+)"\s*\/>/,
  );
  if (!property) throw new Error('missing reactor test classpath');
  const classpath = unescapeXml(property[1]);
  const own = resolve(root, 'services/platform-app/target/test-classes');
  if (!classpath.split(delimiter).some((p) => resolve(p) === own))
    throw new Error('classpath belongs to another worktree');
  // Java defines an empty classpath entry (Surefire may append a delimiter) as
  // the controlled child cwd. Keep those exact semantics, not access('').
  for (const path of classpath.split(delimiter)) await access(path || root);
  const source = join(
    root,
    'services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaHost.java',
  );
  const compiled = join(own, 'auriga/iam/NetworkSourceJavaHost.class');
  if (
    (await stat(source)).mtimeMs > (await stat(compiled)).mtimeMs ||
    (await stat(source)).mtimeMs > (await stat(report)).mtimeMs
  )
    throw new Error('Java bridge classpath/report predates current source');
  return classpath;
}

export async function waitUntil(check, what, timeout = 30_000) {
  const until = Date.now() + timeout;
  for (;;) {
    const value = await check();
    if (value) return value;
    if (Date.now() >= until) throw new Error(what);
    await delay(30);
  }
}
