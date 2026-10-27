/** Production provider preparation is an options object, unlike the prepared
 * register/getTicket methods which take the signal directly. No auth fixture.
 * @param {import('../../../clients/dsh-plugin/src/network/runtime-types.js').DeviceNetworkProvider} provider
 * @param {AbortSignal} signal */
export function prepareDevice(provider, signal) {
  return provider.prepare({ signal });
}
