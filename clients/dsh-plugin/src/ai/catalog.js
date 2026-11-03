import { check, integer, only, text } from './guard.js';

/** Validate the authenticated data-plane directory, independently of invocation.
 * @param {unknown} value
 * @returns {import('./contract.js').components['schemas']['ModelList']['data']} */
export function modelDirectory(value) {
  const list = only(value, ['object', 'data']);
  check(
    list.object === 'list' &&
      Array.isArray(list.data) &&
      list.data.length <= 1000,
    'SERVER',
  );
  const ids = new Set();
  return list.data.map((raw) => {
    const item = only(raw, [
      'id',
      'object',
      'created',
      'owned_by',
      'context_window',
      'max_output_tokens',
    ]);
    text(item.id, 128, 1);
    check(
      /^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(item.id) &&
        !ids.has(item.id) &&
        item.object === 'model' &&
        item.owned_by === 'auriga',
      'SERVER',
    );
    integer(item.created);
    if (item.context_window !== undefined) integer(item.context_window, 2);
    if (item.max_output_tokens !== undefined)
      integer(item.max_output_tokens, 1);
    check(
      item.context_window === undefined || item.context_window <= 1048576,
      'SERVER',
    );
    check(
      item.max_output_tokens === undefined || item.max_output_tokens <= 32768,
      'SERVER',
    );
    check(
      item.context_window === undefined ||
        item.max_output_tokens === undefined ||
        item.max_output_tokens < item.context_window,
      'SERVER',
    );
    ids.add(item.id);
    return {
      id: item.id,
      object: /** @type {const} */ ('model'),
      created: item.created,
      owned_by: /** @type {const} */ ('auriga'),
      ...(item.context_window === undefined
        ? {}
        : { context_window: item.context_window }),
      ...(item.max_output_tokens === undefined
        ? {}
        : { max_output_tokens: item.max_output_tokens }),
    };
  });
}
