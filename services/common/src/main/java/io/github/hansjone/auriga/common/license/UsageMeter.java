package io.github.hansjone.auriga.common.license;

import java.util.Map;

/**
 * Metering hook (model tokens, instance runtime, storage). The default discards records; the
 * metering implementation will publish {@code usage.*} events once that contract exists.
 */
public interface UsageMeter {
  void record(String metric, double amount, Map<String, String> dimensions);

  UsageMeter NOOP = (metric, amount, dimensions) -> {};
}
