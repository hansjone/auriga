// @ts-nocheck
import { SHARED_PROFILE } from './host.js';
import { canonical } from './protocol.js';

const forbiddenServices = [
  'shell',
  'terminal',
  'terminalController',
  'jobs',
  'subagent',
  'mcpClient',
  'pluginManager',
  'ptcRuntime',
];
const forbiddenPlugins = new Set([
  'shell',
  'terminal',
  'terminal-bash',
  'jobs-local',
  'tool-jobs',
  'tool-subagent',
  'tool-subagent-control',
  'subagent',
  'subagent-fork-in-process',
  'subagent-spawn-in-process',
  'mcp-client',
  'mcp-resources',
  'plugin-manager',
]);

/** Public Cordis registry/fiber config describes what is actually mounted in this process. */
export function assertSharedProfile(ctx, expected, skillRoots) {
  if (canonical(expected) !== canonical(SHARED_PROFILE)) return false;
  if (!ctx.registry?.entries || !ctx.reflect?.get) return false;
  if (forbiddenServices.some((key) => ctx.reflect.get(key, true))) return false;
  let nativeTools = false;
  let skills = skillRoots.length === 0;
  for (const [, runtime] of ctx.registry.entries()) {
    const name = runtime.name?.replace(/^@deepseek-ai\/dsh-/, '');
    const fibers = [...runtime.fibers].filter((fiber) => fiber.uid !== null);
    if (fibers.length && forbiddenPlugins.has(name)) return false;
    if (name === 'tools' || name === 'ToolRuntime') {
      if (fibers.length !== 1 || fibers[0].config?.mode !== 'native')
        return false;
      nativeTools = true;
    }
    if (name === 'skill-filesystem') {
      if (fibers.length !== 1) return false;
      const config = fibers[0].config;
      if (
        config?.includeDefaultRoots !== false ||
        config.providerName !== 'auriga-shared' ||
        config.bundledSkillDir !== undefined ||
        !Array.isArray(config.customSkillDirs) ||
        canonical([...config.customSkillDirs].sort()) !==
          canonical([...skillRoots].sort())
      )
        return false;
      skills = true;
    }
  }
  return nativeTools && skills;
}
