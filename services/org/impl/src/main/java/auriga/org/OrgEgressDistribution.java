package auriga.org;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.sql.Timestamp;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.Set;
import java.util.UUID;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Single-installation trusted volume channel. Neither directory is mounted into instances. */
public final class OrgEgressDistribution {
  private final OrgService org;
  private final Clock clock;
  private final UUID tenant;
  private final String node;
  private final Path policies;
  private final Path reports;

  public OrgEgressDistribution(
      OrgService org, Clock clock, UUID tenant, String node, Path policies, Path reports) {
    this.org = org;
    this.clock = clock;
    this.tenant = tenant;
    this.node = node;
    this.policies = policies;
    this.reports = reports;
    if (!node.matches("[a-z0-9][a-z0-9-]{0,62}"))
      throw new IllegalArgumentException("Invalid node");
  }

  public void reconcile() {
    if (tenant == null || policies == null || reports == null) return;
    Delivery delivery =
        org.store()
            .tenant(
                tenant,
                () -> {
                  var store = org.store();
                  store.lockGroupHierarchy();
                  var policy = org.getPolicy("egress");
                  if (!policy.path("configured").asBoolean()) return null;
                  long version = policy.path("version").asLong();
                  var value = policy.path("value");
                  EgressPolicyValidation.validate(value);
                  // Stable field order is part of the content digest; do not hash a jsonb
                  // rendering.
                  var normalized = OrgJson.object("mode", value.path("mode").asString());
                  for (var key : new String[] {"categories", "allowDomains", "denyDomains"})
                    normalized.set(
                        key, value.has(key) ? value.get(key) : OrgJson.MAPPER.createArrayNode());
                  String snapshot =
                      OrgJson.object(
                              "schemaVersion",
                              1,
                              "tenantId",
                              tenant.toString(),
                              "version",
                              version,
                              "value",
                              normalized)
                          .toString();
                  String digest = OrgService.hashCode(snapshot);
                  var previous =
                      store
                          .jdbc()
                          .queryForList(
                              "SELECT version,digest FROM egress_policy_delivery WHERE tenant_id=? ORDER BY version DESC LIMIT 1",
                              tenant);
                  if (!previous.isEmpty()) {
                    long last = ((Number) previous.getFirst().get("version")).longValue();
                    if (last > version
                        || (last == version && !digest.equals(previous.getFirst().get("digest"))))
                      throw new IllegalStateException("Non-monotonic egress policy");
                  }
                  // Publish before committing the delivery marker. Crash recovery rewrites the same
                  // bytes.
                  writeSnapshot(snapshot);
                  store
                      .jdbc()
                      .update(
                          "INSERT INTO egress_policy_delivery(id,tenant_id,version,digest,published_at) VALUES (?,?,?,?,?) ON CONFLICT(tenant_id,version) DO NOTHING",
                          UUID.randomUUID(),
                          tenant,
                          version,
                          digest,
                          Timestamp.from(clock.instant()));
                  return new Delivery(version, digest);
                });
    if (delivery == null) return;
    // A bad heartbeat must never roll back a successfully published candidate.
    try {
      org.store()
          .tenant(
              tenant,
              () -> {
                importReport(delivery.version(), delivery.digest());
                return null;
              });
    } catch (RuntimeException error) {
      org.store()
          .tenant(
              tenant,
              () -> {
                org.store()
                    .jdbc()
                    .update(
                        "UPDATE egress_policy_delivery SET report_error='invalid_report',report_error_at=? WHERE tenant_id=? AND version=?",
                        Timestamp.from(clock.instant()),
                        tenant,
                        delivery.version());
                return null;
              });
    }
  }

  private record Delivery(long version, String digest) {}

  private void writeSnapshot(String snapshot) {
    try {
      Path temporary = Files.createTempFile(policies, ".policy-", ".tmp");
      try {
        Files.writeString(temporary, snapshot);
        if (Files.getFileStore(temporary).supportsFileAttributeView("posix"))
          Files.setPosixFilePermissions(
              temporary, java.nio.file.attribute.PosixFilePermissions.fromString("rw-r--r--"));
        Files.move(
            temporary,
            policies.resolve("policy.json"),
            StandardCopyOption.ATOMIC_MOVE,
            StandardCopyOption.REPLACE_EXISTING);
      } finally {
        Files.deleteIfExists(temporary);
      }
    } catch (IOException error) {
      throw new UncheckedIOException(error);
    }
  }

  private void importReport(long version, String digest) {
    Path file = reports.resolve("report.json");
    if (!Files.exists(file, LinkOption.NOFOLLOW_LINKS)) return;
    try {
      if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.size(file) > 65536)
        throw new IllegalStateException("Invalid report file");
      byte[] bytes;
      try (var stream = Files.newInputStream(file)) {
        bytes = stream.readNBytes(65537);
      }
      if (bytes.length > 65536) throw new IllegalStateException("Oversized report");
      JsonNode report = OrgJson.MAPPER.readTree(bytes);
      // A superseded heartbeat must never replace the current version's status.
      if (report.path("version").isIntegralNumber() && report.path("version").asLong() < version)
        return;
      Set<String> fields =
          Set.of(
              "tenantId", "version", "nodeId", "digest", "result", "appliedVersion", "reportedAt");
      if (!report.isObject()
          || report.size() != fields.size()
          || !report.propertyNames().stream().allMatch(fields::contains)
          || !report.path("version").isIntegralNumber()
          || !report.path("version").canConvertToLong()
          || report.path("version").asLong() != version
          || !tenant.toString().equals(report.path("tenantId").asString())
          || !node.equals(report.path("nodeId").asString())
          || !digest.equals(report.path("digest").asString())
          || !Set.of("applied", "failed").contains(report.path("result").asString())
          || !report.path("appliedVersion").isIntegralNumber()
          || !report.path("appliedVersion").canConvertToLong())
        throw new IllegalStateException("Unbound egress report");
      long applied = report.path("appliedVersion").asLong();
      String result = report.path("result").asString();
      Instant at = Instant.parse(report.path("reportedAt").asString());
      if (applied < 0
          || (result.equals("applied") && applied != version)
          || at.isAfter(clock.instant().plusSeconds(5)))
        throw new IllegalStateException("Invalid egress result");
      org.store()
          .jdbc()
          .update(
              "INSERT INTO egress_policy_report(id,tenant_id,version,node_id,digest,result,applied_version,reported_at,received_at) VALUES (?,?,?,?,?,?,?,?,?)"
                  + " ON CONFLICT(tenant_id,version,node_id) DO UPDATE SET result=EXCLUDED.result,applied_version=EXCLUDED.applied_version,reported_at=EXCLUDED.reported_at,received_at=EXCLUDED.received_at"
                  + " WHERE EXCLUDED.reported_at>egress_policy_report.reported_at AND EXCLUDED.applied_version>=egress_policy_report.applied_version",
              UUID.randomUUID(),
              tenant,
              version,
              node,
              digest,
              result,
              applied,
              Timestamp.from(at),
              Timestamp.from(clock.instant()));
      org.store()
          .jdbc()
          .update(
              "UPDATE egress_policy_delivery SET report_error=NULL,report_error_at=NULL WHERE tenant_id=? AND version=? AND (report_error_at IS NULL OR report_error_at<?)",
              tenant,
              version,
              Timestamp.from(at));
    } catch (IOException error) {
      throw new UncheckedIOException(error);
    }
  }

  public ObjectNode status() {
    if (tenant == null || !tenant.equals(org.store().tenantId()))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    return org.store()
        .tx(
            () -> {
              var policy = org.getPolicy("egress");
              long saved = policy.path("version").asLong();
              var out =
                  OrgJson.object(
                      "savedVersion", Long.toString(saved), "phase", "saved", "nodeId", node);
              out.put("mode", policy.path("value").path("mode").asString());
              out.put("legacyAdjusted", policy.path("legacyAdjusted").asBoolean());
              if (!policy.path("configured").asBoolean()) {
                out.put("phase", "unconfigured");
                return out;
              }
              if (policies == null || reports == null) {
                out.put("phase", "disabled");
                return out;
              }
              var delivery =
                  org.store()
                      .jdbc()
                      .queryForList(
                          "SELECT version,published_at,report_error FROM egress_policy_delivery WHERE tenant_id=? ORDER BY version DESC LIMIT 1",
                          tenant);
              if (!delivery.isEmpty()) {
                var row = delivery.getFirst();
                out.put("distributedVersion", row.get("version").toString());
                out.put(
                    "distributedAt", ((Timestamp) row.get("published_at")).toInstant().toString());
                if (((Number) row.get("version")).longValue() == saved)
                  out.put("phase", "distributed");
              }
              var rows =
                  org.store()
                      .jdbc()
                      .queryForList(
                          "SELECT version,result,applied_version,reported_at FROM egress_policy_report WHERE tenant_id=? AND node_id=? ORDER BY version DESC LIMIT 1",
                          tenant,
                          node);
              if (!rows.isEmpty()) {
                var row = rows.getFirst();
                Instant at = ((Timestamp) row.get("reported_at")).toInstant();
                out.put("appliedVersion", row.get("applied_version").toString());
                out.put("reportedAt", at.toString());
                out.put("result", row.get("result").toString());
                if (((Number) row.get("version")).longValue() == saved) {
                  out.put(
                      "phase",
                      row.get("result").equals("failed")
                          ? "failed"
                          : Duration.between(at, clock.instant()).compareTo(Duration.ofSeconds(90))
                                  > 0
                              ? "stale"
                              : "effective");
                }
              }
              if (!delivery.isEmpty()) {
                var row = delivery.getFirst();
                if (row.get("report_error") != null
                    || ((Number) row.get("version")).longValue() > saved) {
                  out.put("phase", "failed");
                  out.put(
                      "reportError",
                      row.get("report_error") != null
                          ? row.get("report_error").toString()
                          : "policy_version_regression");
                }
              }
              return out;
            });
  }
}
