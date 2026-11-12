// Reference policy for language-neutral lock fixtures, not a production lock implementation.
export function evaluateHostLock(input) {
  const result = (decision, reason) => ({ decision, reason });
  if (input.operation === 'reclaim' && input.callerRole !== 'E')
    return result('reject', 'guardian_only');
  if (!input.owner) return result('wait', 'unverifiable_owner');
  if (input.owner.lockId !== input.observedLockId)
    return result('reject', 'owner_changed');
  if (input.operation === 'release')
    return input.callerRole === input.owner.role &&
      input.callerPid === input.owner.pid
      ? result('allow', 'owner_release')
      : result('reject', 'not_owner');
  if (!input.newStartsBlocked || !input.otherWritersStopped)
    return result('wait', 'quiesce_host_first');
  if (input.holderStatus === 'dead') return result('allow', 'owner_dead');
  if (input.holderStatus !== 'alive')
    return result('wait', 'unverifiable_owner');
  const expired =
    input.bootId === input.owner.bootId &&
    input.monotonicNowMillis - input.owner.monotonicMillis >
      input.owner.leaseDurationMillis;
  if (!expired) return result('wait', 'owner_alive');
  return input.writerStopped
    ? result('allow', 'lease_expired')
    : result('wait', 'stop_writer_first');
}
