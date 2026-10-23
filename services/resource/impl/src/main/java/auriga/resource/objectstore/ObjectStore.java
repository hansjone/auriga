package auriga.resource.objectstore;

import java.io.InputStream;
import java.util.UUID;

/**
 * Internal raw-byte port. Callers must separately authorize and validate archives before commit.
 */
public interface ObjectStore extends AutoCloseable {
  long MAX_BYTES = 64L * 1024 * 1024;

  record StageId(UUID value) {}

  record ObjectInfo(String digest, long sizeBytes) {}

  record Staged(StageId id, ObjectInfo object) {}

  /**
   * Takes ownership of input, including on validation/copy/close failure. Never publishes bytes.
   */
  Staged putStaged(UUID tenant, InputStream input, long maxBytes);

  /** Verified raw stage bytes for a future archive validator. Caller closes before commit/abort. */
  InputStream openStaged(UUID tenant, StageId id);

  /**
   * Consumes this process-local handle on success or failure; never replaces an existing object.
   */
  ObjectInfo commitIfAbsent(UUID tenant, StageId id, ObjectInfo expected);

  /**
   * Verifies the complete bounded file, not just its directory entry. No ACL decision is implied.
   */
  ObjectInfo stat(UUID tenant, String digest);

  /**
   * Verifies before returning an already-open channel; caller owns and closes the returned stream.
   */
  InputStream open(UUID tenant, ObjectInfo expected);

  /** Idempotent for unknown/consumed/other-tenant handles; cannot delete committed objects. */
  void abort(UUID tenant, StageId id);

  /** Only recognized, expired, unlocked stage directories of this tenant are eligible. */
  int cleanupExpiredStaging(UUID tenant);

  /** Aborts local stages. Does not close streams already handed to their caller. */
  @Override
  void close();
}
