import { createDeviceProvider } from '../../../clients/dsh-plugin/src/network/device-provider.js';

/** Each transport-only provider uses coherent metadata for its own fixed
 * origin. The bridge's getDevice port supplies the original IAM credential;
 * it is never decoded/reissued here or validated against a substitute issuer.
 * Gateway still verifies that credential against the genuine IAM authority.
 * @param {{origin:string,network:any,getDevice:Parameters<typeof createDeviceProvider>[0]['getDevice']}} options */
export function assemblyDeviceProvider({ origin, network, getDevice }) {
  return createDeviceProvider({
    config: {
      platformOrigin: origin,
      issuer: origin,
      clientId: 'bridge-device',
      providerId: 'bridge',
      network,
    },
    getDevice,
  });
}

/** Production provider preparation is an options object, unlike the prepared
 * register/getTicket methods which take the signal directly. No auth fixture.
 * @param {import('../../../clients/dsh-plugin/src/network/runtime-types.js').DeviceNetworkProvider} provider
 * @param {AbortSignal} signal */
export function prepareDevice(provider, signal) {
  return provider.prepare({ signal });
}
