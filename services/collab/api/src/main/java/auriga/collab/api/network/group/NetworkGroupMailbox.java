package auriga.collab.api.network.group;

import auriga.collab.api.network.group.NetworkGroups.*;
import auriga.collab.api.network.group.NetworkGroups.Item;
import java.util.UUID;

/**
 * Synchronous database-only sink in the caller's collab transaction/DataSource. Persist one body
 * per message and one node item with per-local recipient IDs; retries must use (messageId,node)
 * uniqueness. Must throw on quota/backpressure, preflight full signed FETCH encoding <=64KiB, and
 * never call NetworkMailboxService.store per page (that would create new
 * messages/sequence/idempotency). HEAD notification belongs after commit. A failed page rolls back
 * its progress and sink writes.
 */
public interface NetworkGroupMailbox {
  void preflight(Item item);

  void store(Item item);

  /**
   * Transactional handoff preserving the provided ID and verified sys source. Enforce ordinary or
   * human_dialogue relationships/first-contact, privacy, all budgets and signed FETCH size. A
   * missing relationship never becomes a deliverable message; do not start a session/model.
   */
  Stored humanUnicast(UUID message, Actor source, HumanSend request);
}
