/** Relay only from the local backend to its OS-authenticated shell peer.
 * Credentials stay in memory and never pass through page status/commands.
 * @param {{identity:any,broker:any,setTimer?:typeof setInterval,clearTimer?:typeof clearInterval}} options */
export function bindLocalUacProof({
  identity,
  broker,
  setTimer = setInterval,
  clearTimer = clearInterval,
}) {
  if (!identity?.proofCredentials || !identity?.subscribe) return () => {};
  let closed = false;
  const publish = () => {
    if (!closed)
      void broker.localUac(identity.proofCredentials()).catch(() => {});
  };
  const off = identity.subscribe(publish);
  const timer = setTimer(publish, 10000);
  timer.unref?.();
  publish();
  return () => {
    closed = true;
    clearTimer(timer);
    off();
    void broker.localUac(null).catch(() => {});
  };
}
