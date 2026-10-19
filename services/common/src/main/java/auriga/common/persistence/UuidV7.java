package auriga.common.persistence;

import java.security.SecureRandom;
import java.time.Clock;
import java.util.UUID;

/** RFC 9562 UUIDv7 (48 bit Unix milliseconds + random), the platform's primary key format. */
public final class UuidV7 {
  private static final SecureRandom RANDOM = new SecureRandom();

  private UuidV7() {}

  public static UUID next() {
    return next(Clock.systemUTC());
  }

  public static UUID next(Clock clock) {
    long millis = clock.millis();
    long randA = RANDOM.nextInt(1 << 12);
    long msb = (millis & 0xFFFFFFFFFFFFL) << 16 | 0x7000L | randA;
    long lsb = (RANDOM.nextLong() & 0x3FFFFFFFFFFFFFFFL) | 0x8000000000000000L;
    return new UUID(msb, lsb);
  }
}
