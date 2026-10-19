package auriga.common.context;

import java.security.SecureRandom;
import java.util.HexFormat;
import java.util.Optional;
import java.util.regex.Pattern;

/**
 * Minimal W3C Trace Context ({@code traceparent}) handling: parse, start a new trace, derive a
 * child span for outgoing calls. Kept dependency free so that HTTP, gRPC and events share it.
 */
public record TraceContext(String traceId, String spanId, String flags) {
  private static final Pattern TRACEPARENT =
      Pattern.compile("^([0-9a-f]{2})-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$");
  private static final SecureRandom RANDOM = new SecureRandom();
  private static final HexFormat HEX = HexFormat.of();

  /** Parses a {@code traceparent} header; invalid or all-zero ids are ignored (spec section 3). */
  public static Optional<TraceContext> parse(String header) {
    if (header == null) return Optional.empty();
    var m = TRACEPARENT.matcher(header.trim());
    if (!m.matches() || "ff".equals(m.group(1))) return Optional.empty();
    String traceId = m.group(2);
    String spanId = m.group(3);
    if (traceId.chars().allMatch(c -> c == '0') || spanId.chars().allMatch(c -> c == '0')) {
      return Optional.empty();
    }
    return Optional.of(new TraceContext(traceId, spanId, m.group(4)));
  }

  /** Continues the incoming trace with a new local span, or starts a new sampled trace. */
  public static TraceContext continueOrStart(String header) {
    return parse(header).map(TraceContext::child).orElseGet(TraceContext::start);
  }

  public static TraceContext start() {
    return new TraceContext(randomHex(16), randomHex(8), "01");
  }

  /** Same trace, new span id: used for the local span and for each outgoing call. */
  public TraceContext child() {
    return new TraceContext(traceId, randomHex(8), flags);
  }

  public String toTraceparent() {
    return "00-" + traceId + "-" + spanId + "-" + flags;
  }

  private static String randomHex(int bytes) {
    byte[] b = new byte[bytes];
    do {
      RANDOM.nextBytes(b);
    } while (allZero(b));
    return HEX.formatHex(b);
  }

  private static boolean allZero(byte[] b) {
    for (byte x : b) if (x != 0) return false;
    return true;
  }
}
