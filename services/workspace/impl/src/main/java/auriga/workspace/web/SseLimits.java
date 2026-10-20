package auriga.workspace.web;

import auriga.common.error.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;

public final class SseLimits {
  public static final int TOTAL = 128, PER_USER = 4;
  private final Map<String, Integer> users = new HashMap<>();
  private int total;

  public synchronized Runnable acquire(String user) {
    if (total >= TOTAL || users.getOrDefault(user, 0) >= PER_USER)
      throw new AurigaException(CommonErrors.RATE_LIMITED);
    users.merge(user, 1, Integer::sum);
    total++;
    var released = new AtomicBoolean();
    return () -> {
      if (released.compareAndSet(false, true))
        synchronized (this) {
          total--;
          users.computeIfPresent(user, (k, v) -> v == 1 ? null : v - 1);
        }
    };
  }
}
