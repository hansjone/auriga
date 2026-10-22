import type { V1IP0IP1IP3 } from '../generated/deployment-access';

/** Only this projection is public; never deliver the installation input. */
export type PublicAccess = Pick<V1IP0IP1IP3, 'mode'>;
export type AccessMode = PublicAccess['mode'];

export function readAccessMode(doc: Document = document): AccessMode {
  const tags = doc.querySelectorAll('meta[name="auriga-access-mode"]');
  const mode = tags[0]?.getAttribute('content');
  if (tags.length !== 1 || (mode !== 'domain' && mode !== 'ip')) {
    throw new Error(
      doc.documentElement.lang.startsWith('en')
        ? 'Access mode is missing or invalid. Administrative writes are blocked.'
        : '入口模式缺失或无效，已禁止管理写操作。',
    );
  }
  return mode;
}
