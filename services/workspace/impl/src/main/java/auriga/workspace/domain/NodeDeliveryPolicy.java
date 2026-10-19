package auriga.workspace.domain;

import java.time.Duration;

public final class NodeDeliveryPolicy {
  public static final int MAX_ATTEMPTS = 5;
  public static final Duration ACK_TIMEOUT = Duration.ofMinutes(2);

  private NodeDeliveryPolicy() {}

  public static Duration backoff(int attempts) {
    return Duration.ofSeconds(Math.min(300, 5L << Math.min(6, Math.max(0, attempts - 1))));
  }

  public static boolean exhausted(int attempts) {
    return attempts >= MAX_ATTEMPTS;
  }
}
