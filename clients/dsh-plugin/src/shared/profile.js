// @ts-nocheck
import { SHARED_PROFILE } from './host.js';
import { canonical } from './protocol.js';

/** Node probes this process through its trusted runtime route before placement admission. */
export function createSharedProfileHandler(ports) {
  return (request, response) => {
    if (request.method !== 'GET' || request.url !== '/_auriga/shared-profile') {
      response.writeHead(404, { 'cache-control': 'no-store' });
      response.end();
      return;
    }
    let valid = false;
    try {
      valid = ports.assertProfile(SHARED_PROFILE) === true;
    } catch {
      // A dependency disappearing must make the next readiness probe fail.
    }
    response.writeHead(valid ? 200 : 503, {
      'content-type': 'application/json',
      'cache-control': 'no-store',
    });
    response.end(
      canonical(
        valid
          ? {
              schemaVersion: 1,
              physicalInstanceId: ports.options.physicalInstanceId,
              profile: 'auriga.shared.v1',
              capabilities: SHARED_PROFILE,
            }
          : { error: 'common.unavailable' },
      ),
    );
  };
}
