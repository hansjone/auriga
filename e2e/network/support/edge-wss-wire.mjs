/* global Buffer */
// Fixed invalid credential vector, validated through the production provider's
// private ingress/subject projection by edge-wss.test.mjs; no auth stub.
export const invalidTicketVector = Object.freeze({
  ticket: 'a'.repeat(43),
  ingress: 'm3ju7otr7d2fb57ekwmikpstx4',
  upSubject: 'auriga.net.up.u.m3ju7otr7d2fb57ekwmikpstx4',
});

export function createWireObserver() {
  let pending = '',
    bytes = 0,
    frames = 0,
    overflow = false,
    stopped = false;
  let info = null,
    authError = null,
    close = null,
    conflictingInfo = false;
  const encodings = { arraybuffer: 0, view: 0, string: 0, other: 0 };
  return {
    message(data) {
      if (stopped || overflow) return;
      let buffer;
      if (data instanceof ArrayBuffer) {
        encodings.arraybuffer++;
        buffer = Buffer.from(data);
      } else if (ArrayBuffer.isView(data)) {
        encodings.view++;
        buffer = Buffer.from(data.buffer, data.byteOffset, data.byteLength);
      } else if (typeof data === 'string') {
        encodings.string++;
        buffer = Buffer.from(data);
      } else {
        encodings.other++;
        overflow = true;
        return;
      }
      bytes += buffer.length;
      frames++;
      if (bytes > 65536 || frames > 256) {
        overflow = true;
        pending = '';
        return;
      }
      pending += buffer.toString('utf8');
      if (pending.length > 16384) {
        overflow = true;
        pending = '';
        return;
      }
      for (let end; (end = pending.indexOf('\r\n')) >= 0;) {
        const line = pending.slice(0, end);
        pending = pending.slice(end + 2);
        if (line.startsWith('INFO ')) {
          try {
            const value = JSON.parse(line.slice(5));
            if (
              /^N[A-Z2-7]{55}$/.test(value.server_id) &&
              Number.isSafeInteger(value.client_id) &&
              value.client_id > 0
            ) {
              const identity = {
                serverId: value.server_id,
                clientId: String(value.client_id),
              };
              if (info && JSON.stringify(info) !== JSON.stringify(identity))
                conflictingInfo = true;
              else info = identity;
            }
          } catch {
            /* Invalid server INFO cannot become connection evidence. */
          }
        } else if (
          /^-ERR (?:'Authorization Violation'|"Authorization Violation"|Authorization Violation)$/.test(
            line,
          )
        ) {
          authError = 'authorization-violation';
        } else if (/^(?:H?MSG) /.test(line)) {
          // Never scan message bodies (which could contain arbitrary strings).
          stopped = true;
          pending = '';
          break;
        }
      }
    },
    close(code, reason) {
      const label = Buffer.from(reason).toString('utf8');
      close = {
        code,
        reason:
          label === 'Authentication Failure'
            ? 'authentication-failure'
            : label === 'Authentication Timeout'
              ? 'authentication-timeout'
              : 'other',
      };
    },
    evidence() {
      return {
        info,
        authError,
        close,
        frames,
        bytes,
        encodings: { ...encodings },
        overflow,
        conflictingInfo,
      };
    },
  };
}

export function brokerRejected(wire) {
  return (
    wire?.overflow === false &&
    wire.conflictingInfo === false &&
    (wire.authError === 'authorization-violation' ||
      (wire.close?.code === 1008 &&
        wire.close.reason === 'authentication-failure'))
  );
}
