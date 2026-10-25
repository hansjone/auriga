/**
 * System local (0) intake channel: first-contact / prompts for the human
 * approval UI. Never forwards into a model session context.
 *
 * @typedef {{
 *   recipientId: string,
 *   messageId: string,
 *   kind: string,
 *   from: unknown,
 *   threadId: string,
 *   previewText: string,
 *   contactId: string|null,
 *   acceptedAt: string,
 * }} SystemInboxItem
 */

/**
 * @param {{ maxItems?: number }} [options]
 */
export function createSystemInbox({ maxItems = 256 } = {}) {
  /** @type {SystemInboxItem[]} */
  const items = [];
  /** @type {Set<(item: SystemInboxItem) => void>} */
  const listeners = new Set();

  return {
    /** Bounded pending prompts (newest last). */
    list() {
      return items.slice();
    },

    /**
     * @param {(item: SystemInboxItem) => void} listener
     * @returns {() => void}
     */
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },

    /**
     * Record an inbox_only system acceptance. Idempotent on recipientId.
     * @param {SystemInboxItem} item
     */
    offer(item) {
      if (items.some((x) => x.recipientId === item.recipientId)) return false;
      items.push(item);
      while (items.length > maxItems) items.shift();
      for (const l of listeners) l(item);
      return true;
    },

    /** @param {string} recipientId */
    dismiss(recipientId) {
      const i = items.findIndex((x) => x.recipientId === recipientId);
      if (i >= 0) items.splice(i, 1);
    },
  };
}

/**
 * Build a UI prompt from a delivered page item + system receipt.
 * @param {import('../host/adapter.js').PageItem} item
 * @param {import('../host/adapter.js').Receipt} receipt
 * @param {string} acceptedAt
 * @returns {import('./system-inbox.js').SystemInboxItem}
 */
export function systemPromptFrom(item, receipt, acceptedAt) {
  const body = /** @type {{ text?: unknown } | null | undefined} */ (
    item.message.body
  );
  const text = typeof body?.text === 'string' ? body.text : '';
  return {
    recipientId: receipt.recipientId,
    messageId: item.message.messageId,
    kind: item.message.kind,
    from: item.message.from,
    threadId: item.message.threadId,
    previewText: text.slice(0, 512),
    contactId:
      item.message.kind === 'first_contact' ? item.message.messageId : null,
    acceptedAt,
  };
}
