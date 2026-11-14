package auriga.iam;

import auriga.audit.api.UacAuditEvents;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.security.AuthorizationRules;
import auriga.ops.api.UacOpsAlerts;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.*;
import java.time.format.DateTimeFormatter;
import java.util.*;
import java.util.concurrent.Semaphore;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Collect one explicit tenant, re-project every source, then authorize the finished download. */
final class UacDiagnosticBundles {
  static final int MAX_BYTES = 50 * 1024 * 1024;

  record Bundle(String filename, byte[] bytes, boolean partial) {}

  private record Source(boolean available, String reason, boolean truncated) {}

  private record Selection(UUID provider, UUID correlation, int seconds) {}

  private final IamStore store;
  private final UacProviderService providers;
  private final UacSessions uac;
  private final UacDiagnosticSettings settings;
  private final UacAuditEvents audit;
  private final UacOpsAlerts ops;
  private final UacLokiSource loki;
  private final EventPublisher events;
  private final Clock clock;
  private final Path temporaryRoot;
  private final int limit;
  private final boolean ssoCookieEnabled;
  private final Semaphore concurrent = new Semaphore(2);

  UacDiagnosticBundles(
      IamStore store,
      UacProviderService providers,
      UacSessions uac,
      UacDiagnosticSettings settings,
      UacAuditEvents audit,
      UacOpsAlerts ops,
      UacLokiSource loki,
      EventPublisher events,
      Clock clock,
      boolean ssoCookieEnabled,
      Path temporaryRoot) {
    this(
        store,
        providers,
        uac,
        settings,
        audit,
        ops,
        loki,
        events,
        clock,
        ssoCookieEnabled,
        temporaryRoot,
        MAX_BYTES);
  }

  UacDiagnosticBundles(
      IamStore store,
      UacProviderService providers,
      UacSessions uac,
      UacDiagnosticSettings settings,
      UacAuditEvents audit,
      UacOpsAlerts ops,
      UacLokiSource loki,
      EventPublisher events,
      Clock clock,
      boolean ssoCookieEnabled,
      Path temporaryRoot,
      int limit) {
    if (limit < 256 * 1024 || limit > MAX_BYTES)
      throw new IllegalArgumentException("Invalid diagnostic size limit");
    this.store = store;
    this.providers = providers;
    this.uac = uac;
    this.settings = settings;
    this.audit = audit;
    this.ops = ops;
    this.loki = loki;
    this.events = events;
    this.clock = clock;
    this.ssoCookieEnabled = ssoCookieEnabled;
    this.temporaryRoot = temporaryRoot;
    this.limit = limit;
  }

  private static Selection selection(JsonNode input) {
    IamError.validate(input.isObject());
    input
        .propertyNames()
        .forEach(
            key ->
                IamError.validate(
                    Set.of("providerId", "correlationId", "sinceSeconds").contains(key)));
    try {
      UUID provider =
          input.has("providerId") ? UacBundleRedactor.id(input.path("providerId")) : null;
      UUID correlation =
          input.has("correlationId") ? UacBundleRedactor.id(input.path("correlationId")) : null;
      IamError.validate(
          !input.has("sinceSeconds")
              || input.path("sinceSeconds").isIntegralNumber()
                  && input.path("sinceSeconds").canConvertToInt());
      int seconds = input.path("sinceSeconds").asInt(7200);
      IamError.validate(seconds >= 1 && seconds <= 86400);
      return new Selection(provider, correlation, seconds);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  Bundle build(JsonNode input, Runnable authorize) {
    Selection selection = selection(input);
    authorize.run();
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    UUID tenant = TenantContext.require();
    if (!tenant.equals(principal.tenantId())
        || !principal.isAdminContext()
        || principal.type() != auriga.common.security.PrincipalType.USER
        || !principal
            .permissions()
            .containsAll(Set.of("iam.providers.read", "audit.events.read", "ops.alerts.read")))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    store.limit("uac-diagnostic-export:" + principal.subject(), clock.instant(), 3);
    if (!concurrent.tryAcquire()) throw new AurigaException(CommonErrors.RATE_LIMITED);
    try {
      return collect(selection, tenant, authorize);
    } catch (IOException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } finally {
      concurrent.release();
    }
  }

  private Bundle collect(Selection selection, UUID tenant, Runnable authorize) throws IOException {
    Instant to = clock.instant(), from = to.minusSeconds(selection.seconds());
    List<UUID> selected =
        store.tx(
            () -> {
              if (selection.provider() != null) {
                providers.row(selection.provider().toString(), false);
                return List.of(selection.provider());
              }
              return store
                  .networkJdbc()
                  .query(
                      "SELECT id FROM iam.idp_provider WHERE tenant_id=? AND data->>'connectorType'='uac' ORDER BY id LIMIT 101",
                      (rs, n) -> rs.getObject(1, UUID.class),
                      tenant);
            });
    boolean providerTruncated = selected.size() > 100;
    if (providerTruncated) selected = selected.subList(0, 100);
    if (selected.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
    Set<UUID> allowed = Set.copyOf(selected);
    Map<UUID, Long> detailRevisions = new HashMap<>();
    for (UUID provider : selected) {
      long revision = settings.capture(provider.toString());
      if (revision >= 0) detailRevisions.put(provider, revision);
    }
    int content = limit - 128 * 1024;
    Lines steps = new Lines((int) (content * .42)),
        auditLines = new Lines((int) (content * .20)),
        detail = new Lines((int) (content * .30));
    Map<String, Source> sources = new LinkedHashMap<>();
    Set<String> seen = new HashSet<>();
    Counts counts = new Counts();
    boolean iamTruncated = false;
    try {
      UUID cursor = new UUID(0, 0);
      for (int page = 0; page < 20; page++) {
        UUID after = cursor;
        String lower = from.toString().substring(0, 19), upper = to.toString().substring(0, 19);
        List<ObjectNode> rows =
            store.tx(
                () ->
                    store
                        .networkJdbc()
                        .query(
                            "SELECT id,data->'event' AS event FROM iam.uac_diagnostics WHERE tenant_id=? AND data->>'kind'='event' AND id>? AND substring(data->'event'->>'timestamp',1,19)>=? AND substring(data->'event'->>'timestamp',1,19)<=? ORDER BY id LIMIT 500",
                            (rs, n) ->
                                Json.object(
                                    "id",
                                    rs.getString("id"),
                                    "event",
                                    Json.MAPPER.readTree(rs.getString("event"))),
                            tenant,
                            after,
                            lower,
                            upper));
        for (ObjectNode row : rows)
          consumeStage(
              row.path("event"),
              tenant,
              allowed,
              selection,
              from,
              to,
              detailRevisions,
              seen,
              steps,
              detail,
              counts);
        if (rows.size() < 500) break;
        cursor = UUID.fromString(rows.getLast().path("id").asString());
        if (page == 19) iamTruncated = true;
      }
      sources.put(
          "iam",
          new Source(
              true,
              counts.invalid > 0
                  ? "invalid_log"
                  : iamTruncated || providerTruncated ? "truncated" : "available",
              iamTruncated || providerTruncated));
    } catch (RuntimeException failure) {
      authorizeFailure(failure);
      sources.put("iam", new Source(false, "unreachable", false));
    }
    if (loki == null) sources.put("loki", new Source(false, "missing", false));
    else {
      var read = loki.read(tenant, selection.provider(), selection.correlation(), from, to);
      int before = counts.invalid;
      for (String line : read.lines())
        try {
          consumeStage(
              UacBundleRedactor.parse(line),
              tenant,
              allowed,
              selection,
              from,
              to,
              detailRevisions,
              seen,
              steps,
              detail,
              counts);
        } catch (RuntimeException invalid) {
          counts.invalid++;
        }
      sources.put(
          "loki",
          new Source(
              read.available(),
              counts.invalid > before ? "invalid_log" : read.reason(),
              read.truncated()));
    }
    boolean auditTruncated = false;
    int invalidAudit = 0;
    if (audit == null) sources.put("audit", new Source(false, "missing", false));
    else
      try {
        for (UUID provider : selected) {
          var read =
              audit.readUac(
                  from,
                  to,
                  provider,
                  selection.correlation(),
                  Math.max(1, 10000 - auditLines.lines));
          auditTruncated |= read.truncated();
          for (String line : read.events())
            try {
              var record = UacBundleRedactor.audit(UacBundleRedactor.parse(line));
              if (!tenant.toString().equals(record.path("tenantId").asString())
                  || !provider.toString().equals(record.path("target").path("id").asString())
                  || !within(record.path("time").asString(), from, to)
                  || selection.correlation() != null
                      && !selection
                          .correlation()
                          .toString()
                          .equals(record.path("details").path("correlationId").asString())) {
                invalidAudit++;
                continue;
              }
              auditLines.add(record);
            } catch (RuntimeException invalid) {
              invalidAudit++;
            }
          if (auditLines.lines >= 10000) {
            auditTruncated = true;
            break;
          }
        }
        sources.put(
            "audit",
            new Source(
                true,
                invalidAudit > 0 ? "invalid_log" : auditTruncated ? "truncated" : "available",
                auditTruncated));
      } catch (RuntimeException failure) {
        authorizeFailure(failure);
        sources.put("audit", new Source(false, "unreachable", false));
      }
    List<ObjectNode> alerts = new ArrayList<>();
    boolean opsTruncated = false;
    int invalidOps = 0;
    if (ops == null) sources.put("ops", new Source(false, "missing", false));
    else
      try {
        for (UUID provider : selected) {
          var read = ops.readUac(from, to, provider, Math.max(1, 1000 - alerts.size()));
          opsTruncated |= read.truncated();
          for (String line : read.alerts())
            try {
              var record = UacBundleRedactor.ops(UacBundleRedactor.parse(line));
              if (!provider.toString().equals(record.path("providerId").asString())
                  || Instant.parse(record.path("firstSeen").asString()).isAfter(to)
                  || Instant.parse(record.path("lastSeen").asString()).isBefore(from)) {
                invalidOps++;
                continue;
              }
              alerts.add(record);
            } catch (RuntimeException invalid) {
              invalidOps++;
            }
          if (alerts.size() >= 1000) {
            opsTruncated = true;
            break;
          }
        }
        sources.put(
            "ops",
            new Source(
                true,
                invalidOps > 0 ? "invalid_log" : opsTruncated ? "truncated" : "available",
                opsTruncated));
      } catch (RuntimeException failure) {
        authorizeFailure(failure);
        sources.put("ops", new Source(false, "unreachable", false));
      }
    if (steps.lines == 0 && auditLines.lines == 0 && alerts.isEmpty())
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    ObjectNode config = config(selected, counts, invalidAudit, invalidOps);
    LinkedHashMap<String, byte[]> files = new LinkedHashMap<>();
    Map<String, Boolean> truncated = new HashMap<>();
    files.put("uac-steps.jsonl", steps.bytes());
    truncated.put(
        "uac-steps.jsonl",
        steps.truncated || iamTruncated || sources.get("loki").truncated() || counts.invalid > 0);
    files.put("audit.jsonl", auditLines.bytes());
    truncated.put("audit.jsonl", auditLines.truncated || auditTruncated || invalidAudit > 0);
    byte[] opsBytes = encode(Json.object("items", alerts));
    while (opsBytes.length > (int) (content * .02) && !alerts.isEmpty()) {
      alerts = new ArrayList<>(alerts.subList(0, alerts.size() / 2));
      opsBytes = encode(Json.object("items", alerts, "truncated", true));
      opsTruncated = true;
    }
    files.put("ops-alerts.json", opsBytes);
    truncated.put("ops-alerts.json", opsTruncated || invalidOps > 0);
    byte[] configBytes = encode(config);
    boolean configTruncated = configBytes.length > (int) (content * .06);
    if (configTruncated)
      configBytes = encode(Json.object("truncated", true, "statistics", counts.json()));
    files.put("sanitized-config.json", configBytes);
    truncated.put("sanitized-config.json", configTruncated || providerTruncated);
    // A setting may expire during collection. Historical detail stays excluded unless it is
    // still authorized by the same captured diagnostics revision at publication.
    boolean detailAllowed = !detailRevisions.isEmpty();
    for (var entry : detailRevisions.entrySet())
      detailAllowed &= settings.permits(entry.getKey().toString(), entry.getValue());
    if (detailAllowed && detail.lines > 0) {
      files.put("uds-detail.jsonl", detail.bytes());
      truncated.put("uds-detail.jsonl", detail.truncated);
    }
    boolean partial =
        sources.values().stream().anyMatch(s -> !s.available() || !s.reason().equals("available"))
            || truncated.values().stream().anyMatch(Boolean::booleanValue);
    authorize.run();
    boolean finalPartial = partial;
    List<UUID> finalProviders = List.copyOf(selected);
    UUID exportId =
        store.tx(
            () -> {
              var principal =
                  AuthorizationRules.currentPrincipal()
                      .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
              UUID bundle = auriga.common.persistence.UuidV7.next();
              return events.publish(
                  "audit.recorded",
                  bundle.toString(),
                  Json.object(
                      "actor",
                      Json.object("type", "user", "id", principal.subject()),
                      "action",
                      "iam.uac.diagnostics.export",
                      "target",
                      Json.object("type", "diagnostic_bundle", "id", bundle.toString()),
                      "result",
                      "success",
                      "details",
                      Json.object(
                          "from",
                          from.toString(),
                          "to",
                          to.toString(),
                          "providerIds",
                          finalProviders.stream().map(UUID::toString).toList(),
                          "correlationId",
                          selection.correlation() == null
                              ? null
                              : selection.correlation().toString(),
                          "partial",
                          finalPartial)),
                  "1");
            });
    if (exportId == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    List<ObjectNode> manifestFiles = new ArrayList<>();
    for (var entry : files.entrySet())
      manifestFiles.add(
          Json.object(
              "name",
              entry.getKey(),
              "sha256",
              hash(entry.getValue()),
              "lines",
              entry.getKey().endsWith("jsonl") ? lineCount(entry.getValue()) : 1,
              "bytes",
              entry.getValue().length,
              "truncated",
              truncated.getOrDefault(entry.getKey(), false)));
    String version = UacDiagnosticBundles.class.getPackage().getImplementationVersion();
    if (version == null || !version.matches("[A-Za-z0-9._+-]{1,128}")) version = "development";
    var manifest =
        Json.object(
            "version",
            1,
            "from",
            from.toString(),
            "to",
            to.toString(),
            "redactorVersion",
            UacDiagnosticSanitizer.VERSION,
            "exportAuditId",
            exportId.toString(),
            "partial",
            partial,
            "missingSources",
            sources.entrySet().stream()
                .filter(e -> !e.getValue().available())
                .map(Map.Entry::getKey)
                .toList(),
            "files",
            manifestFiles,
            "installationVersion",
            version,
            "componentVersions",
            Map.of("iam", version),
            "sources",
            sources.entrySet().stream()
                .map(
                    e ->
                        Json.object(
                            "source",
                            e.getKey(),
                            "available",
                            e.getValue().available(),
                            "reason",
                            e.getValue().reason()))
                .toList());
    new ContractSchemas()
        .validate("auth/uac-control.schema.json#/$defs/UacDiagnosticManifest", manifest);
    files.put("manifest.json", encode(manifest));
    byte[] zip;
    try (var temporary = new UacDiagnosticTemp(temporaryRoot)) {
      zip = temporary.publish(files, limit);
      authorize.run();
      if (files.containsKey("uds-detail.jsonl"))
        for (var entry : detailRevisions.entrySet())
          if (!settings.permits(entry.getKey().toString(), entry.getValue()))
            throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    String filename =
        "uac-diagnostics-"
            + DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'")
                .withZone(ZoneOffset.UTC)
                .format(to)
            + "-"
            + UUID.randomUUID()
            + ".zip";
    return new Bundle(filename, zip, partial);
  }

  private void consumeStage(
      JsonNode input,
      UUID tenant,
      Set<UUID> providers,
      Selection selection,
      Instant from,
      Instant to,
      Map<UUID, Long> detailRevisions,
      Set<String> seen,
      Lines steps,
      Lines detail,
      Counts counts) {
    try {
      var stage = UacBundleRedactor.stage(input, false);
      UUID provider = UacBundleRedactor.id(stage.path("providerId"));
      if (!tenant.toString().equals(stage.path("tenantId").asString())
          || !providers.contains(provider)
          || !within(stage.path("timestamp").asString(), from, to)
          || selection.correlation() != null
              && !selection
                  .correlation()
                  .toString()
                  .equals(stage.path("correlationId").asString())) {
        counts.outOfScope++;
        counts.invalid++;
        return;
      }
      if (!seen.add(stage.path("eventId").asString())) return;
      if (stage.path("level").asString().equals("DEBUG")) {
        Long revision = detailRevisions.get(provider);
        if (revision != null) detail.add(UacBundleRedactor.stage(input, true));
      } else {
        steps.add(stage);
        counts.add(stage);
      }
    } catch (RuntimeException invalid) {
      counts.invalid++;
    }
  }

  private ObjectNode config(List<UUID> selected, Counts counts, int invalidAudit, int invalidOps) {
    return store.tx(
        () -> {
          List<ObjectNode> projection = new ArrayList<>();
          for (UUID provider : selected) {
            ObjectNode row = providers.row(provider.toString(), false),
                config = uac.providers.decryptedConfig(row);
            var preview =
                store
                    .networkJdbc()
                    .query(
                        "SELECT data::text FROM iam.session WHERE tenant_id=? AND provider_id=? AND data->>'uacCredentialId' IS NOT NULL ORDER BY id DESC LIMIT 21",
                        (rs, n) -> Json.read(rs.getString(1)),
                        TenantContext.require(),
                        provider);
            List<ObjectNode> sessions = new ArrayList<>();
            for (ObjectNode session : preview.subList(0, Math.min(20, preview.size()))) {
              var safe =
                  Json.object(
                      "sessionId",
                      session.path("sessionId"),
                      "authGeneration",
                      session.path("authGeneration"),
                      "revoked",
                      session.path("revoked"),
                      "departmentPresent",
                      session.path("uacLoginInfo").has("department"));
              UacTelemetry.copySafe(session, safe, false);
              String state = session.path("uacLoginInfo").path("status").asString();
              if (Set.of("pending", "available", "unavailable").contains(state))
                safe.put("loginInfoStatus", state);
              if (session.path("uacLoginInfo").path("updatedAt").isString())
                safe.put(
                    "loginInfoUpdatedAt",
                    Instant.parse(session.path("uacLoginInfo").path("updatedAt").asString())
                        .toString());
              sessions.add(safe);
            }
            var retry =
                store
                    .networkJdbc()
                    .query(
                        "SELECT data::text FROM iam.uac_credential WHERE tenant_id=? AND data->>'providerId'=? AND data->>'kind'='credential' ORDER BY id DESC LIMIT 21",
                        (rs, n) -> Json.read(rs.getString(1)),
                        TenantContext.require(),
                        provider.toString());
            List<ObjectNode> retries = new ArrayList<>();
            for (ObjectNode credential : retry.subList(0, Math.min(20, retry.size()))) {
              var safe = Json.object("tokenReplaced", credential.path("version").asLong() > 1);
              UacTelemetry.copySafe(credential, safe, false);
              retries.add(safe);
            }
            var pending =
                store
                    .networkJdbc()
                    .queryForObject(
                        "SELECT count(*) FROM iam.uac_job WHERE tenant_id=? AND data->>'providerId'=? AND COALESCE(data->>'done','false')<>'true'",
                        Long.class,
                        TenantContext.require(),
                        provider.toString());
            projection.add(
                Json.object(
                    "providerId",
                    provider.toString(),
                    "enabled",
                    row.path("enabled"),
                    "sourceAlias",
                    "uac",
                    "endpoints",
                    List.of("scan", "department", "token-verify"),
                    "systemCodePresent",
                    !config.path("systemCode").asString().isBlank(),
                    "ssoCookieEnabled",
                    ssoCookieEnabled,
                    "adapterCapabilities",
                    capabilities(),
                    "revalidation",
                    uac.policy.policy(),
                    "sessionPreview",
                    sessions,
                    "credentialSchedule",
                    retries,
                    "scheduleTruncated",
                    preview.size() > 20 || retry.size() > 20,
                    "pendingJobs",
                    pending));
          }
          return Json.object(
              "providers",
              projection,
              "statistics",
              counts.json(),
              "omittedAudit",
              invalidAudit,
              "omittedOps",
              invalidOps,
              "redactorVersion",
              UacDiagnosticSanitizer.VERSION);
        });
  }

  private ObjectNode capabilities() {
    ObjectNode safe = Json.object();
    Map<String, Boolean> declared = uac.adapter.diagnosticCapabilities();
    for (String key :
        List.of("portalCookieConfigured", "udsCookieConfigured", "originSystemCodeEmpty")) {
      Boolean value = declared.get(key);
      if (value == null) safe.putNull(key);
      else safe.put(key, value);
    }
    return safe;
  }

  private static boolean within(String time, Instant from, Instant to) {
    Instant value = Instant.parse(time);
    return !value.isBefore(from) && !value.isAfter(to);
  }

  private static void authorizeFailure(RuntimeException failure) {
    if (failure instanceof AurigaException error
        && Set.of("common.unauthenticated", "common.forbidden").contains(error.errorCode().code()))
      throw error;
  }

  private static byte[] encode(JsonNode value) {
    return value.toString().getBytes(StandardCharsets.UTF_8);
  }

  private static String hash(byte[] bytes) {
    try {
      return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    } catch (java.security.NoSuchAlgorithmException e) {
      throw new IllegalStateException("Required digest unavailable");
    }
  }

  private static int lineCount(byte[] value) {
    int count = 0;
    for (byte b : value) if (b == '\n') count++;
    return count;
  }

  private static final class Lines {
    final int cap;
    final ByteArrayOutputStream data = new ByteArrayOutputStream();
    int lines;
    boolean truncated;

    Lines(int cap) {
      this.cap = cap;
    }

    void add(JsonNode value) {
      byte[] bytes = encode(value);
      if (bytes.length + 1 > cap - data.size()) {
        truncated = true;
        return;
      }
      data.writeBytes(bytes);
      data.write('\n');
      lines++;
    }

    byte[] bytes() {
      return data.toByteArray();
    }
  }

  private static final class Counts {
    int invalid, outOfScope, missing, suppressed, fallback;
    final Map<String, int[]> purposes = new LinkedHashMap<>();

    Counts() {
      for (String purpose : List.of("login_info", "login_sso", "revalidate"))
        purposes.put(purpose, new int[2]);
    }

    void add(ObjectNode event) {
      if (!event.path("phase").asString().equals("end")) return;
      String stage = event.path("stage").asString();
      if (Set.of("token_verify", "department_lookup").contains(stage)) {
        int[] value = purposes.get(event.path("purpose").asString());
        if (value != null) value[event.path("result").asString().equals("success") ? 0 : 1]++;
      }
      if (stage.equals("sso_cookie_check")) {
        if (event.path("fallbackReason").asString().equals("missing")) missing++;
        if (event.path("fallbackReason").asString().equals("suppressed")) suppressed++;
        if (event.path("result").asString().equals("scan_required")) fallback++;
      }
    }

    ObjectNode json() {
      ObjectNode result =
          Json.object(
              "invalidRecords",
              invalid,
              "outOfScopeRecords",
              outOfScope,
              "ssoMissing",
              missing,
              "ssoSuppressed",
              suppressed,
              "ssoFallback",
              fallback);
      ObjectNode counts = Json.object();
      purposes.forEach(
          (key, value) -> counts.set(key, Json.object("success", value[0], "failure", value[1])));
      result.set("purposes", counts);
      return result;
    }
  }
}
