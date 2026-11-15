/** Transient fetch failure only; validation and persistence errors never use it. */
export class DeviceControlUnavailable extends Error {}
/** Only proven credential invalidation may end the native login.
 * A network permission/quota denial says nothing about the model credential.
 * @param {unknown} error */
export function isDeviceRevocationError(error) {
  const value = /** @type {{status?:number,code?:string}|null} */ (error);
  return value?.code === 'network.revoked';
}
