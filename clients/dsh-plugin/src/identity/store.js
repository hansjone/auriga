const MAX_RATE_BUCKETS = 4096;

/**
 * In-memory persistence for standalone mode (owner, identities, sessions, rate limits).
 * Persistent adapters MUST implement claimOwner atomically (compare-and-set).
 */
export function createMemoryStore() {
  /** @type {{
   *   owner: null | { providerId: string, subject: string, displayName?: string, email?: string },
   *   identities: Map<string, { providerId: string, subject: string, linkedAt: string }>,
   *   sessions: Map<string, Record<string, unknown>>,
   *   rateLimits: Map<string, { count: number, expiresAt: number }>,
   *   emergencyLastCounter: number,
   * }} */
  const data = {
    owner: null,
    identities: new Map(),
    sessions: new Map(),
    rateLimits: new Map(),
    emergencyLastCounter: -1,
  };

  /** @param {number} nowMs */
  function sweepRateLimits(nowMs) {
    for (const [key, bucket] of data.rateLimits) {
      if (bucket.expiresAt <= nowMs) data.rateLimits.delete(key);
    }
    while (data.rateLimits.size > MAX_RATE_BUCKETS) {
      const oldest = data.rateLimits.keys().next().value;
      if (oldest == null) break;
      data.rateLimits.delete(oldest);
    }
  }

  return {
    getOwner() {
      return data.owner;
    },
    /**
     * Atomic claim: only succeeds when no owner exists yet.
     * @param {{ providerId: string, subject: string, displayName?: string, email?: string }} owner
     * @returns {boolean} true if this call became the owner
     */
    claimOwner(owner) {
      if (data.owner) return false;
      data.owner = { ...owner };
      return true;
    },
    /**
     * @deprecated use claimOwner — kept for diagnostics only
     * @param {{ providerId: string, subject: string, displayName?: string, email?: string }} owner
     */
    setOwner(owner) {
      data.owner = { ...owner };
    },
    /** @param {string} providerId @param {string} subject */
    identityKey(providerId, subject) {
      return `${providerId}:${subject}`;
    },
    /** @param {string} providerId @param {string} subject */
    hasIdentity(providerId, subject) {
      return data.identities.has(this.identityKey(providerId, subject));
    },
    /**
     * @param {string} providerId
     * @param {string} subject
     * @param {string} [linkedAt]
     */
    putIdentity(providerId, subject, linkedAt = new Date().toISOString()) {
      data.identities.set(this.identityKey(providerId, subject), {
        providerId,
        subject,
        linkedAt,
      });
    },
    listIdentities() {
      return [...data.identities.values()];
    },
    /** @param {string} id @param {Record<string, unknown>} session */
    putSession(id, session) {
      data.sessions.set(id, session);
    },
    /** @param {string} id */
    getSession(id) {
      return data.sessions.get(id) ?? null;
    },
    /** @param {string} id */
    deleteSession(id) {
      data.sessions.delete(id);
    },
    getEmergencyLastCounter() {
      return data.emergencyLastCounter;
    },
    /** @param {number} counter */
    setEmergencyLastCounter(counter) {
      data.emergencyLastCounter = counter;
    },
    /**
     * @param {string} key
     * @param {number} nowMs
     * @param {number} maximum
     * @returns {boolean} true if allowed
     */
    consumeRateLimit(key, nowMs, maximum) {
      sweepRateLimits(nowMs);
      const bucket = `${key}:${Math.floor(nowMs / 1000 / 300)}`;
      const existing = data.rateLimits.get(bucket);
      if (!existing || existing.expiresAt <= nowMs) {
        if (data.rateLimits.size >= MAX_RATE_BUCKETS) sweepRateLimits(nowMs);
        if (data.rateLimits.size >= MAX_RATE_BUCKETS) return false;
        data.rateLimits.set(bucket, { count: 1, expiresAt: nowMs + 600_000 });
        return true;
      }
      if (existing.count >= maximum) return false;
      existing.count += 1;
      return true;
    },
    /** @internal test helper */
    rateLimitSize() {
      return data.rateLimits.size;
    },
  };
}

/**
 * Normalize a username for rate-limit keys (trim, lower-case, cap length).
 * @param {string} username
 */
export function normalizeRateLimitUser(username) {
  return username.trim().toLowerCase().slice(0, 128);
}
