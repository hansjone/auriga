import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { lstat, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { parseStrictJson } from '../../clients/dsh-plugin/src/network/trust/jcs.js';

const mainClass = 'auriga.collab.network.keys.MsgnetInstallationOperator';
const loader = fileURLToPath(
  new URL('./msgnet-empty-loader.properties', import.meta.url),
);
const actions = new Set(['prepare', 'sign', 'finalize', 'migration-plan']);
const invalid = () => {
  throw new Error('Invalid fixed msgnet installation launcher input');
};
const exact = (value, fields) =>
  value &&
  typeof value === 'object' &&
  !Array.isArray(value) &&
  Object.keys(value).sort().join(',') === [...fields].sort().join(',');

async function localFile(input, privateFile = false) {
  if (
    typeof input !== 'string' ||
    !path.isAbsolute(input) ||
    path.normalize(input) !== input ||
    input.startsWith('\\\\') ||
    input.startsWith('//')
  )
    invalid();
  let cursor = input;
  for (;;) {
    const stat = await lstat(cursor);
    if (stat.isSymbolicLink()) invalid();
    if (
      cursor === input &&
      (!stat.isFile() ||
        stat.nlink !== 1 ||
        (process.platform !== 'win32' &&
          stat.mode & (privateFile ? 0o077 : 0o022)))
    )
      invalid();
    const parent = path.dirname(cursor);
    if (parent === cursor) break;
    cursor = parent;
  }
  return input;
}

async function pinned(ref, kind) {
  if (!exact(ref, ['path', 'sha256']) || !/^[a-f0-9]{64}$/.test(ref.sha256))
    invalid();
  const file = await localFile(ref.path);
  if (
    kind === 'java' &&
    !new RegExp(`^java${process.platform === 'win32' ? '\\.exe' : ''}$`).test(
      path.basename(file),
    )
  )
    invalid();
  const digest = createHash('sha256');
  for await (const chunk of createReadStream(file)) digest.update(chunk);
  if (digest.digest('hex') !== ref.sha256) invalid();
  return file;
}

/** Resolves only pinned local Java/jar files; callers cannot choose a main class or JVM options. */
export async function installationInvocation(
  action,
  launcherFile,
  planFile,
  environment = process.env,
) {
  if (!actions.has(action)) invalid();
  for (const name of Object.keys(environment)) {
    if (
      /^(?:JAVA_TOOL_OPTIONS|JDK_JAVA_OPTIONS|_JAVA_OPTIONS|CLASSPATH|JAVA_OPTS|LOADER_.*)$/i.test(
        name,
      )
    )
      invalid();
  }
  await localFile(launcherFile, true);
  if ((await lstat(launcherFile)).size > 65536) invalid();
  const config = parseStrictJson(await readFile(launcherFile));
  if (!exact(config, ['version', 'java', 'jar']) || config.version !== 1)
    invalid();
  const java = await pinned(config.java, 'java');
  const jar = await pinned(config.jar, 'jar');
  await localFile(planFile, true);
  await localFile(loader);
  if (
    (await readFile(loader, 'utf8')) !==
    '# Intentionally empty: the installation launcher supplies one fixed main class.\n'
  )
    invalid();
  const env = {};
  // No inherited Java/loader configuration, service credentials or arbitrary process environment.
  for (const name of ['SystemRoot', 'WINDIR', 'TEMP', 'TMP']) {
    if (environment[name]) env[name] = environment[name];
  }
  env.LANG = 'C.UTF-8';
  return {
    file: java,
    args: [
      '-Xmx512m',
      '-XX:MaxMetaspaceSize=256m',
      '-XX:ActiveProcessorCount=2',
      `-Dloader.main=${mainClass}`,
      `-Dloader.config.location=${pathToFileURL(loader).href}`,
      '-cp',
      jar,
      'org.springframework.boot.loader.launch.PropertiesLauncher',
      action,
      planFile,
    ],
    env,
  };
}

async function main(args) {
  if (args.length !== 3) invalid();
  const invocation = await installationInvocation(...args);
  const child = spawn(invocation.file, invocation.args, {
    env: invocation.env,
    stdio: 'inherit',
    windowsHide: true,
    shell: false,
  });
  process.exitCode = await new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', (code, signal) => resolve(signal ? 1 : (code ?? 1)));
  });
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  main(process.argv.slice(2)).catch(() => {
    process.stderr.write(
      'Msgnet 安装启动器拒绝操作；请核对本地工具摘要、受保护计划及固定命令。\n',
    );
    process.exitCode = 1;
  });
}
