import { standalone } from './modes/standalone.js';
import { managed } from './modes/managed.js';
import { connect } from './modes/connect.js';
/** @typedef {import('./generated/steps').ClientStep} ClientStep */
/** @typedef {'standalone' | 'managed' | 'connect'} Mode */
export const PROTOCOL_VERSION = 1;
/** @param {NodeJS.ProcessEnv} [env] @returns {Mode} */
export function selectMode(env = process.env) {
  const mode = env.AURIGA_MODE ?? 'standalone';
  if (mode !== 'standalone' && mode !== 'managed' && mode !== 'connect')
    throw new Error('Invalid AURIGA_MODE: ' + mode);
  return mode;
}
/** Select a placeholder implementation. No DSH hooks or network operations exist yet.
 * @param {NodeJS.ProcessEnv} [env]
 */
export default function initialize(env = process.env) {
  return { standalone, managed, connect }[selectMode(env)]();
}
