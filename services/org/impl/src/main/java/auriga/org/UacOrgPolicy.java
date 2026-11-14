package auriga.org;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.iam.api.IdentityPolicyHooks;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import tools.jackson.databind.JsonNode;

/** Only trusted deployment records can supply an employee or invitation binding. */
final class UacOrgPolicy {
  record Redemption(UUID invitation, UUID provider, String employee, UUID account) {}

  private static final ScopedValue<Redemption> REDEMPTION = ScopedValue.newInstance();
  private final OrgStore store;
  private final OrgService org;
  private final Clock clock;

  UacOrgPolicy(OrgStore store, OrgService org, Clock clock) {
    this.store = store;
    this.org = org;
    this.clock = clock;
  }

  static String employee(String value) {
    return auriga.iam.api.UacEmployee.canonical(value);
  }

  Optional<UUID> resolve(UUID provider, String employee) {
    String subject = employee(employee);
    return store.tx(
        () ->
            store
                .jdbc()
                .query(
                    "SELECT b.account_id FROM uac_employee_binding b JOIN member m ON m.tenant_id=b.tenant_id AND m.id=b.account_id WHERE b.tenant_id=? AND b.provider_id=? AND b.employee=? AND m.status='active'",
                    (rs, i) -> (UUID) rs.getObject(1),
                    store.tenantId(),
                    provider,
                    subject)
                .stream()
                .findFirst());
  }

  Optional<IdentityPolicyHooks.UacMember> member(UUID account) {
    return store.tx(
        () ->
            store
                .member(account)
                .filter(m -> "active".equals(m.status()))
                .map(m -> new IdentityPolicyHooks.UacMember(m.displayName(), m.memberType())));
  }

  boolean consume(String code, UUID provider, String employee, UUID account) {
    if (code == null || code.isBlank() || account == null) return false;
    String subject = employee(employee);
    try {
      return store.tx(
          () -> {
            // Same order as target deletion and the generic redemption path.
            store.lockGroupHierarchy();
            var rows =
                store
                    .jdbc()
                    .queryForList(
                        "SELECT i.id,i.max_uses FROM invitation i JOIN uac_invitation_employee u ON u.tenant_id=i.tenant_id AND u.invitation_id=i.id WHERE i.tenant_id=? AND i.code_hash=? AND u.provider_id=? AND u.employee=? AND u.purpose='uac_login' AND (u.active OR i.status='used') FOR UPDATE OF i,u",
                        store.tenantId(),
                        OrgService.hashCode(code),
                        provider,
                        subject);
            if (rows.size() != 1 || ((Number) rows.getFirst().get("max_uses")).intValue() != 1)
              return false;
            UUID invitation = (UUID) rows.getFirst().get("id");
            var bindings =
                store
                    .jdbc()
                    .queryForList(
                        "SELECT account_id FROM uac_employee_binding WHERE tenant_id=? AND provider_id=? AND employee=? FOR UPDATE",
                        store.tenantId(),
                        provider,
                        subject);
            if (!bindings.isEmpty() && !account.equals(bindings.getFirst().get("account_id")))
              return false;
            ScopedValue.where(REDEMPTION, new Redemption(invitation, provider, subject, account))
                .run(() -> org.redeemInvitation(code, account, "", null, provider));
            if (store.member(account).filter(m -> "active".equals(m.status())).isEmpty())
              throw new AurigaException(CommonErrors.FORBIDDEN);
            if (bindings.isEmpty())
              store
                  .jdbc()
                  .update(
                      "INSERT INTO uac_employee_binding(id,tenant_id,provider_id,employee,account_id,verified_by,verified_at) SELECT ?,tenant_id,provider_id,employee,?,verified_by,verified_at FROM uac_invitation_employee WHERE tenant_id=? AND invitation_id=?",
                      UuidV7.next(),
                      account,
                      store.tenantId(),
                      invitation);
            return true;
          });
    } catch (AurigaException e) {
      return false;
    }
  }

  static void requireRedemption(OrgStore store, UUID invitation, UUID provider, UUID account) {
    var rows =
        store
            .jdbc()
            .queryForList(
                "SELECT provider_id,employee FROM uac_invitation_employee WHERE tenant_id=? AND invitation_id=?",
                store.tenantId(),
                invitation);
    if (rows.isEmpty()) return;
    Redemption claim = REDEMPTION.isBound() ? REDEMPTION.get() : null;
    if (claim == null
        || !claim.invitation().equals(invitation)
        || !claim.provider().equals(provider)
        || !claim.account().equals(account)
        || !claim.provider().equals(rows.getFirst().get("provider_id"))
        || !claim.employee().equals(rows.getFirst().get("employee")))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  /** Bounded, private one-time provisioning; removing this file does not unlink identities. */
  void provision(Path path) {
    if (path == null) return;
    final JsonNode input;
    try {
      if (Files.isSymbolicLink(path)
          || !Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS)
          || Files.size(path) > 1024 * 1024) throw new IllegalArgumentException();
      input = OrgJson.MAPPER.readTree(Files.readAllBytes(path));
      if (!input.isArray() || input.size() > 10000) throw new IllegalArgumentException();
    } catch (Exception e) {
      throw new IllegalArgumentException("Invalid private UAC member provisioning file");
    }
    for (JsonNode record : input) {
      Set<String> fields = new HashSet<>();
      record.propertyNames().forEach(fields::add);
      if (!record.isObject()
          || !Set.of(
                  "tenantId",
                  "providerId",
                  "employee",
                  "accountId",
                  "invitationId",
                  "purpose",
                  "verifiedBy",
                  "verifiedAt",
                  "verified")
              .containsAll(fields)
          || !record.path("verified").asBoolean()
          || record.has("accountId") == record.has("invitationId"))
        throw new IllegalArgumentException("Invalid UAC verification record");
      UUID tenant = UUID.fromString(record.path("tenantId").asString()),
          provider = UUID.fromString(record.path("providerId").asString()),
          by = UUID.fromString(record.path("verifiedBy").asString());
      String subject = employee(record.path("employee").asString());
      Instant at = Instant.parse(record.path("verifiedAt").asString());
      if (at.isAfter(clock.instant().plusSeconds(30)))
        throw new IllegalArgumentException("UAC verification time is in the future");
      store.tenant(
          tenant,
          () -> {
            store.lockGroupHierarchy();
            if (record.has("accountId")) {
              UUID account = UUID.fromString(record.path("accountId").asString());
              if (store.member(account).filter(m -> "active".equals(m.status())).isEmpty())
                throw new IllegalArgumentException("UAC verification member is not active");
              store
                  .jdbc()
                  .update(
                      "INSERT INTO uac_employee_binding(id,tenant_id,provider_id,employee,account_id,verified_by,verified_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(tenant_id,provider_id,employee) DO NOTHING",
                      UuidV7.next(),
                      tenant,
                      provider,
                      subject,
                      account,
                      by,
                      java.sql.Timestamp.from(at));
              if (!resolve(provider, subject).orElseThrow().equals(account))
                throw new IllegalArgumentException("Conflicting UAC member verification");
            } else {
              if (!"uac_login".equals(record.path("purpose").asString()))
                throw new IllegalArgumentException("Invalid UAC invitation purpose");
              UUID invitation = UUID.fromString(record.path("invitationId").asString());
              store
                  .jdbc()
                  .update(
                      "UPDATE uac_invitation_employee u SET active=false FROM invitation i"
                          + " WHERE u.tenant_id=? AND u.provider_id=? AND u.employee=? AND u.active"
                          + " AND i.tenant_id=u.tenant_id AND i.id=u.invitation_id"
                          + " AND (i.status<>'active' OR i.uses>=i.max_uses OR i.expires_at<=?)",
                      tenant,
                      provider,
                      subject,
                      java.sql.Timestamp.from(clock.instant()));
              var invites =
                  store
                      .jdbc()
                      .queryForList(
                          "SELECT max_uses,uses,status,expires_at FROM invitation WHERE tenant_id=? AND id=? FOR UPDATE",
                          tenant,
                          invitation);
              if (invites.size() != 1
                  || ((Number) invites.getFirst().get("max_uses")).intValue() != 1)
                throw new IllegalArgumentException("UAC invitation must be single use");
              var invite = invites.getFirst();
              boolean active =
                  "active".equals(invite.get("status"))
                      && ((Number) invite.get("uses")).intValue() < 1
                      && clock
                          .instant()
                          .isBefore(((java.sql.Timestamp) invite.get("expires_at")).toInstant());
              store
                  .jdbc()
                  .update(
                      "INSERT INTO uac_invitation_employee(id,tenant_id,invitation_id,provider_id,employee,purpose,verified_by,verified_at,active) VALUES (?,?,?,?,?,'uac_login',?,?,?) ON CONFLICT(tenant_id,invitation_id) DO UPDATE SET active=EXCLUDED.active",
                      UuidV7.next(),
                      tenant,
                      invitation,
                      provider,
                      subject,
                      by,
                      java.sql.Timestamp.from(at),
                      active);
              var bound =
                  store
                      .jdbc()
                      .queryForList(
                          "SELECT provider_id,employee FROM uac_invitation_employee WHERE tenant_id=? AND invitation_id=?",
                          tenant,
                          invitation)
                      .getFirst();
              if (!provider.equals(bound.get("provider_id"))
                  || !subject.equals(bound.get("employee")))
                throw new IllegalArgumentException("Conflicting UAC invitation verification");
            }
            return null;
          });
    }
  }
}
