package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.resource.app.ResourceAccess;
import auriga.resource.app.ResourceCursor;
import java.security.SecureRandom;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class ResourceCursorTest {
  static final Instant NOW = Instant.parse("2026-10-24T00:00:00Z");

  static byte[] key() {
    var bytes = new byte[32];
    new SecureRandom().nextBytes(bytes);
    return bytes;
  }

  static ResourceAccess.Reader reader() {
    return new ResourceAccess.Reader(UUID.randomUUID(), UUID.randomUUID(), false, Set.of(), true);
  }

  @Test
  void expiredTamperedAndWrongKeyCursorsAreRejected() {
    byte[] key = key();
    var cursors = new ResourceCursor(key, Clock.fixed(NOW, ZoneOffset.UTC));
    var reader = reader();
    var position = cursors.initial(UUID.randomUUID());
    String encoded = cursors.encode(reader, "packages", 50, position);
    assertEquals(position, cursors.decode(encoded, reader, "packages", 50));
    var expired = new ResourceCursor(key, Clock.fixed(NOW.plusSeconds(900), ZoneOffset.UTC));
    assertInvalid(() -> expired.decode(encoded, reader, "packages", 50));
    assertInvalid(
        () ->
            new ResourceCursor(key(), Clock.fixed(NOW, ZoneOffset.UTC))
                .decode(encoded, reader, "packages", 50));
    String tampered = (encoded.charAt(0) == 'A' ? "B" : "A") + encoded.substring(1);
    assertInvalid(() -> cursors.decode(tampered, reader, "packages", 50));
    assertInvalid(() -> cursors.decode(encoded + "=", reader, "packages", 50));
    assertInvalid(() -> cursors.decode("a".repeat(2049), reader, "packages", 50));
  }

  @Test
  void cursorsBindTenantSubjectScopeAndLimit() {
    var cursors = new ResourceCursor(key(), Clock.fixed(NOW, ZoneOffset.UTC));
    var reader = reader();
    String encoded =
        cursors.encode(
            reader, "versions/" + UUID.randomUUID(), 5, cursors.initial(UUID.randomUUID()));
    assertInvalid(() -> cursors.decode(encoded, reader, "packages", 5));
    String packages = cursors.encode(reader, "packages", 5, cursors.initial(UUID.randomUUID()));
    assertInvalid(
        () ->
            cursors.decode(
                packages,
                new ResourceAccess.Reader(
                    reader.tenant(), UUID.randomUUID(), false, Set.of(), true),
                "packages",
                5));
    assertInvalid(
        () ->
            cursors.decode(
                packages,
                new ResourceAccess.Reader(
                    UUID.randomUUID(), reader.account(), false, Set.of(), true),
                "packages",
                5));
    assertInvalid(() -> cursors.decode(packages, reader, "packages", 6));
  }

  @Test
  void emptyInputStartsAListAndShortKeysCannotEnablePagination() {
    var cursors = new ResourceCursor(key(), Clock.fixed(NOW, ZoneOffset.UTC));
    assertNull(cursors.decode(null, reader(), "packages", 50));
    assertNull(cursors.decode("", reader(), "packages", 50));
    assertThrows(
        IllegalArgumentException.class, () -> new ResourceCursor(new byte[31], Clock.systemUTC()));
  }

  private static void assertInvalid(Runnable action) {
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, action::run).errorCode().code());
  }
}
