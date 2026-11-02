/** Declare the managed owner's privileged settings seat through DSH's public
 * transport composition hook. Install only inside the signed owner door AND
 * the live managed-credential guard; this flag grants no server authority.
 * The ordinary HTTP/WebSocket carrier stays in place. Never alter Host facts
 * on an already running client or apply this to connect/standalone pages.
 * @param {any} ctx
 */
export function installManagedSettingsOwnership(ctx) {
  if (typeof ctx.on !== 'function') {
    ctx.logger?.warn?.(
      'auriga-dsh: managed settings ownership bootstrap unavailable',
    );
    return () => {};
  }
  return ctx.on('webserver/index-inject', (/** @type {any[]} */ table) => {
    if (
      table.some(
        (row) => row.kind === 'global' && row.name === '__DSH_TRANSPORT__',
      )
    )
      throw new Error('auriga-dsh: conflicting transport ownership bootstrap');
    table.push({
      kind: 'global',
      name: '__DSH_TRANSPORT__',
      value: { ownsHost: true },
    });
  });
}
