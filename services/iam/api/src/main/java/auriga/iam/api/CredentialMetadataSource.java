package auriga.iam.api;

import java.time.Instant;
import java.util.List;

/** Public metadata only. Implementations must never retain token, secret or private-key bytes. */
public interface CredentialMetadataSource {
  String name();

  Snapshot snapshot();

  record Entry(
      String id,
      String type,
      String source,
      String versionLabel,
      Instant issuedAt,
      Instant expiresAt,
      String status) {}

  record Gap(String source, String reason) {}

  record Snapshot(List<Entry> entries, List<Gap> gaps) {
    public Snapshot {
      entries = List.copyOf(entries);
      gaps = List.copyOf(gaps);
    }
  }
}
