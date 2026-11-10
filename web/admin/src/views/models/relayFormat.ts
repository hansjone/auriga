import { shallowRef } from 'vue';
import { orgApi } from '../../api';
import { readAllPages } from '../../admin/pagination';
import type { RelayKey } from '../../admin/relayApi';

const MICROS = 1_000_000;

/** USD micros → editable dollar amount (exact for any safe integer budget). */
export function usdFromMicros(micros: number) {
  return micros / MICROS;
}

/** Dollar amount typed by an administrator → integer micros, or null if invalid. */
export function microsFromUsd(usd: unknown) {
  if (typeof usd !== 'number' || !Number.isFinite(usd) || usd < 0) return null;
  const micros = Math.round(usd * MICROS);
  return Number.isSafeInteger(micros) ? micros : null;
}

/** Display a micros amount as dollars, trimming trailing zeros but keeping cents. */
export function formatUsd(micros: number | null | undefined) {
  if (
    micros === null ||
    micros === undefined ||
    !Number.isSafeInteger(micros) ||
    micros < 0
  )
    return null;
  const whole = Math.trunc(micros / MICROS);
  const fraction = String(micros % MICROS)
    .padStart(6, '0')
    .replace(/0+$/, '')
    .padEnd(2, '0');
  return `$${whole.toLocaleString('en-US')}.${fraction}`;
}

const dateTime = new Intl.DateTimeFormat(undefined, {
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
});
export function formatDateTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : dateTime.format(date);
}

export const shortId = (value: string) =>
  value.length > 13 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;

/**
 * Best-effort member display names for relay keys. Usage and key pages stay
 * usable with raw account IDs when the member directory cannot be read.
 */
export function useMemberNames() {
  const names = shallowRef(new Map<string, string>());
  async function load(signal?: AbortSignal) {
    try {
      const members = await readAllPages(
        (cursor) => orgApi.listMembers({ cursor, limit: 100 }),
        signal,
      );
      names.value = new Map(members.map((m) => [m.id, m.displayName]));
    } catch {
      /* directory unavailable: fall back to account IDs */
    }
  }
  const memberName = (accountId: string) =>
    names.value.get(accountId) ?? shortId(accountId);
  const keyLabel = (key: RelayKey) =>
    key.source === 'member' ? memberName(key.accountId) : key.name;
  return { names, load, memberName, keyLabel };
}
