import { join } from 'node:path';

/** Git's usr/bin must never shadow Windows archive or ACL tools. */
export const systemTool = (name) =>
  process.platform === 'win32'
    ? join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', name + '.exe')
    : name;
