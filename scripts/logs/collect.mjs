#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { mkdir, open, readdir, writeFile, lstat } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { createReadStream } from 'node:fs';
import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';
import {
  mask,
  logDirectory,
} from '../../clients/dsh-plugin/src/observability/index.js';

const cap = 128 * 1024 * 1024;
export function options(args) {
  const out = {
    project: 'auriga-m2',
    since: new Date(Date.now() - 3600000).toISOString(),
    until: new Date().toISOString(),
    pluginDir: logDirectory(),
  };
  for (let i = 0; i < args.length; i += 2) {
    const key = {
      '--project': 'project',
      '--since': 'since',
      '--until': 'until',
      '--trace-id': 'trace',
      '--out': 'directory',
      '--plugin-dir': 'pluginDir',
      '--docker-context': 'dockerContext',
    }[args[i]];
    if (!key || !args[i + 1])
      throw new Error(
        '参数需成对：--project --since --until --trace-id --out --plugin-dir --docker-context',
      );
    out[key] = args[i + 1];
  }
  if (out.trace && !/^[0-9a-f]{32}$/.test(out.trace))
    throw new Error('trace id 必须是32位小写十六进制');
  if (
    ![out.since, out.until].every(
      (v) =>
        /^\d{4}-\d\d-\d\dT.*(?:Z|[+-]\d\d:\d\d)$/.test(v) &&
        Number.isFinite(Date.parse(v)),
    ) ||
    Date.parse(out.since) > Date.parse(out.until)
  )
    throw new Error('时间必须为ISO8601，且since不晚于until');
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(out.project))
    throw new Error('非法compose project');
  out.directory = resolve(out.directory ?? `auriga-logs-${Date.now()}`);
  return out;
}
/** Docker stdout/stderr are both log streams; never inspect environment/config/secrets. */
async function command(args, accept) {
  return new Promise((resolveCommand) => {
    const child = spawn('docker', args, {
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
      shell: false,
    });
    let bytes = 0,
      truncated = false,
      reason;
    const timer = setTimeout(() => {
      reason = 'command_timeout';
      child.kill();
    }, 30000);
    const readers = [child.stdout, child.stderr].map(async (stream) => {
      for await (const line of createInterface({
        input: stream,
        crlfDelay: Infinity,
      })) {
        bytes += Buffer.byteLength(line);
        if (bytes > cap) {
          truncated = true;
          child.kill();
          break;
        }
        await accept(mask(line));
      }
    });
    child.on('error', (error) => {
      reason = error.code ?? 'spawn_failed';
    });
    child.on('close', async (code, signal) => {
      clearTimeout(timer);
      const read = await Promise.allSettled(readers);
      resolveCommand({
        exit_code: code,
        signal,
        truncated,
        bytes,
        reason,
        reader_errors: read
          .filter((v) => v.status === 'rejected')
          .map(() => 'read_failed'),
      });
    });
  });
}
export async function collect(config, run = command) {
  await mkdir(config.directory, { recursive: false, mode: 0o700 });
  const manifest = {
    version: 1,
    started_at: new Date().toISOString(),
    project: config.project,
    since: config.since,
    until: config.until,
    trace_id: config.trace ?? null,
    scope:
      'docker logs returns retained container stdout/stderr for this compose project; trace filtering is local; plugin files are finite local retained files, not proof of end-to-end completeness',
    sources: [],
  };
  let failures = 0;
  const docker = config.dockerContext
    ? ['--context', config.dockerContext]
    : [];
  const containers = [];
  const discovered = await run(
    [
      ...docker,
      'ps',
      '-a',
      '--filter',
      `label=com.docker.compose.project=${config.project}`,
      '--format',
      '{{.ID}} {{.Label "com.docker.compose.service"}}',
    ],
    (line) => containers.push(line),
  );
  manifest.sources.push({ source: 'docker-discovery', ...discovered });
  if (discovered.exit_code !== 0 || !containers.length) failures++;
  for (const row of containers) {
    const [id, service] = row.split(' ');
    if (!/^[0-9a-f]{12,64}$/.test(id) || !/^[a-zA-Z0-9_.-]+$/.test(service)) {
      failures++;
      continue;
    }
    const file = `docker-${service}-${id}.log`;
    const handle = await open(join(config.directory, file), 'wx', 0o600);
    let selected = 0;
    const args = [
      ...docker,
      'logs',
      '--timestamps',
      '--since',
      config.since,
      '--until',
      config.until,
      id,
    ];
    let serial = Promise.resolve();
    const result = await run(args, (line) => {
      // Local exact trace match, after Docker's time-window selection.
      if (config.trace) {
        try {
          if (
            JSON.parse(line.slice(line.indexOf('{'))).trace_id !== config.trace
          )
            return;
        } catch {
          return;
        }
      }
      selected++;
      serial = serial.then(() => handle.writeFile(mask(line) + '\n'));
      return serial;
    });
    await serial;
    await handle.close();
    manifest.sources.push({
      source: 'docker',
      id,
      service,
      file,
      selected,
      command: ['docker', ...args],
      ...result,
    });
    if (
      result.exit_code !== 0 ||
      result.truncated ||
      result.reader_errors.length
    )
      failures++;
  }
  let pluginFiles = [];
  try {
    pluginFiles = (await readdir(config.pluginDir)).filter((name) =>
      /^auriga-dsh-[0-7]\.jsonl(?:\.1)?$/.test(name),
    );
  } catch (error) {
    manifest.sources.push({
      source: 'plugin',
      missing: true,
      reason: error.code ?? 'unavailable',
    });
    failures++;
  }
  if (!pluginFiles.length) {
    manifest.sources.push({
      source: 'plugin',
      missing: true,
      reason: 'no_retained_files',
    });
    failures++;
  }
  for (const name of pluginFiles) {
    let handle;
    try {
      const input = join(config.pluginDir, name);
      if (!(await lstat(input)).isFile()) {
        manifest.sources.push({
          source: 'plugin',
          file: name,
          reason: 'not_regular_file',
        });
        failures++;
        continue;
      }
      handle = await open(join(config.directory, name), 'wx', 0o600);
      let selected = 0,
        invalid = 0;
      for await (const line of createInterface({
        input: createReadStream(input),
        crlfDelay: Infinity,
      })) {
        let event;
        try {
          event = JSON.parse(line);
        } catch {
          invalid++;
          continue;
        }
        const at = Date.parse(event['@timestamp']);
        if (!Number.isFinite(at)) {
          invalid++;
          continue;
        }
        if (
          at < Date.parse(config.since) ||
          at > Date.parse(config.until) ||
          (config.trace && event.trace_id !== config.trace)
        )
          continue;
        selected++;
        await handle.writeFile(mask(line) + '\n');
      }
      await handle.close();
      manifest.sources.push({
        source: 'plugin',
        file: name,
        selected,
        invalid,
      });
      if (invalid) failures++;
    } catch (error) {
      failures++;
      manifest.sources.push({
        source: 'plugin',
        file: name,
        missing: true,
        reason: error.code ?? 'read_failed',
      });
    } finally {
      await handle?.close().catch(() => {});
    }
  }
  manifest.finished_at = new Date().toISOString();
  manifest.exit_code = failures ? 2 : 0;
  await writeFile(
    join(config.directory, 'manifest.json'),
    JSON.stringify(manifest, null, 2) + '\n',
    { mode: 0o600 },
  );
  return manifest;
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  try {
    const config = options(process.argv.slice(2));
    const result = await collect(config);
    console.log(`采集目录：${config.directory}；退出码：${result.exit_code}`);
    process.exitCode = result.exit_code;
  } catch (error) {
    console.error(mask(error.message));
    process.exitCode = 1;
  }
}
