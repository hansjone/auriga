import { realpath } from 'node:fs/promises';
import { isAbsolute, relative, resolve, sep } from 'node:path';

/** Public directoryPicker capability adapter; upstream browse has no root config.
 * This constrains the picker, not arbitrary filesystem operations by user tools.
 * @param {any} picker @param {string} root
 */
export function guardDirectoryPicker(picker, root) {
  if (typeof picker?.capability !== 'function') return () => {};
  const original = picker.capability;
  const base = resolve(root);
  const inside = (/** @type {string} */ path, boundary = base) => {
    const part = relative(boundary, path);
    return part !== '..' && !part.startsWith('..' + sep) && !isAbsolute(part);
  };
  const forbidden = () =>
    Object.assign(new Error('common.forbidden'), { code: 'common.forbidden' });
  const checked = async (/** @type {string} */ path) => {
    if (typeof path !== 'string' || !isAbsolute(path) || !inside(resolve(path)))
      throw forbidden();
    if (!inside(await realpath(path), await realpath(base))) throw forbidden();
    return path;
  };
  const capability = original.call(picker);
  if (!capability) return () => {};
  const guarded =
    capability.kind === 'browse'
      ? {
          ...capability,
          async list(
            /** @type {string|undefined} */ path,
            /** @type {AbortSignal|undefined} */ signal,
          ) {
            const listing = await capability.list(
              await checked(path ?? base),
              signal,
            );
            const entries = [];
            for (const entry of listing.entries) {
              try {
                await checked(entry.path);
                entries.push(entry);
              } catch {
                /* Out-of-root or disappearing directory is not a choice. */
              }
            }
            return {
              ...listing,
              home: base,
              crumbs: listing.crumbs.filter((/** @type {any} */ p) =>
                inside(p.path),
              ),
              entries,
            };
          },
          async createDirectory(
            /** @type {string} */ path,
            /** @type {string} */ name,
          ) {
            if (
              typeof name !== 'string' ||
              !name.trim() ||
              name === '.' ||
              name === '..' ||
              /[/\\]/.test(name)
            )
              throw forbidden();
            const created = await capability.createDirectory(
              await checked(path),
              name,
            );
            return checked(created);
          },
        }
      : {
          ...capability,
          async pick(/** @type {AbortSignal} */ signal) {
            const picked = await capability.pick(signal);
            return picked === null ? null : checked(picked);
          },
        };
  const getCapability = () => guarded;
  picker.capability = getCapability;
  return () => {
    if (picker.capability === getCapability) picker.capability = original;
  };
}
