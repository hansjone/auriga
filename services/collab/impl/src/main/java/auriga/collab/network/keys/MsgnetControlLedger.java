package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.Map;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Frozen installation pins are supplied by the installation/authentication adapter. */
public final class MsgnetControlLedger {
  public record Identity(
      String targetId,
      String identityId,
      String signingSpki,
      String component,
      String gatewayId,
      String sealingSpki,
      String subject) {}

  private final MsgnetRenewal renewal;
  private final JdbcTemplate jdbc;
  private final UUID tenant;
  private final String realm;
  private final Map<String, Identity> identities;
  private final Map<String, JsonNode> nodePins;

  private record Target(String targetId, String identityId, String signingSpki, String revision) {}

  private Target target(String id) {
    var pin = identities.get(id);
    if (pin != null) return new Target(pin.targetId(), pin.identityId(), pin.signingSpki(), "0");
    var node = nodePins.get(id);
    require(node != null, "scope_denied");
    return new Target(
        text(node, "target_id"),
        text(node, "identity_id"),
        text(node, "signing_spki"),
        text(node, "identity_revision"));
  }

  public MsgnetControlLedger(
      MsgnetRenewal renewal,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      Map<String, Identity> identities) {
    this(renewal, jdbc, tenant, realm, identities, Map.of());
  }

  public MsgnetControlLedger(
      MsgnetRenewal renewal,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      Map<String, Identity> identities,
      Map<String, JsonNode> nodePins) {
    this.renewal = renewal;
    this.jdbc = jdbc;
    this.tenant = tenant;
    this.realm = realm;
    this.identities = Map.copyOf(identities);
    this.nodePins = Map.copyOf(nodePins);
    var ids = new java.util.HashSet<String>();
    identities.values().forEach(pin -> ids.add(pin.identityId()));
    nodePins.forEach(
        (id, pin) ->
            require(
                !identities.containsKey(id) && ids.add(text(pin, "identity_id")), "scope_denied"));
  }

  Identity identity(String identityId) {
    return identities.values().stream()
        .filter(i -> i.identityId().equals(identityId))
        .findFirst()
        .orElseThrow(() -> failure("scope_denied"));
  }

  static Map<String, Identity> pins(JsonNode source, UUID tenant, String realm) {
    schema("InstallationPins", source, "invalid_chain");
    require(
        tenant.toString().equals(text(source, "tenant_id")) && realm.equals(text(source, "realm")),
        "scope_denied");
    var pins = new java.util.HashMap<String, Identity>();
    var ids = new java.util.HashSet<String>();
    for (var value : source.path("pins")) {
      var pin =
          new Identity(
              text(value, "target_id"),
              text(value, "identity_id"),
              text(value, "signing_spki"),
              text(value, "component"),
              text(value, "gateway_id"),
              text(value, "sealing_spki"),
              text(value, "subject"));
      canonicalSpki(pin.signingSpki(), "Ed25519");
      canonicalSpki(pin.sealingSpki(), "X25519");
      try {
        require(
            new javax.security.auth.x500.X500Principal(pin.subject())
                .getName(javax.security.auth.x500.X500Principal.RFC2253)
                .equals(pin.subject()),
            "invalid_chain");
      } catch (IllegalArgumentException invalidSubject) {
        throw failure("invalid_chain");
      }
      require(
          pins.putIfAbsent(pin.targetId(), pin) == null && ids.add(pin.identityId()),
          "scope_denied");
    }
    return Map.copyOf(pins);
  }

  static void canonicalSpki(String encoded, String algorithm) {
    try {
      byte[] bytes = Base64.getUrlDecoder().decode(encoded);
      var key =
          java.security.KeyFactory.getInstance(algorithm)
              .generatePublic(new java.security.spec.X509EncodedKeySpec(bytes));
      require(
          Base64.getUrlEncoder().withoutPadding().encodeToString(key.getEncoded()).equals(encoded),
          "invalid_chain");
      String name =
          key instanceof java.security.interfaces.EdECPublicKey ed
              ? ed.getParams().getName()
              : key instanceof java.security.interfaces.XECPublicKey x
                      && x.getParams() instanceof java.security.spec.NamedParameterSpec spec
                  ? spec.getName()
                  : "";
      require(algorithm.equals(name), "invalid_chain");
    } catch (java.security.GeneralSecurityException | IllegalArgumentException e) {
      throw failure("invalid_chain");
    }
  }

  void requireUsable(Identity identity) {
    requireProjectionAllowed(identity);
    require(
        "ready".equals(text(row(identity.targetId()).path("isolation"), "state")),
        "identity_quarantined");
  }

  /** Confirmed clean installations may only fetch/project their scoped recovery material. */
  void requireProjectionAllowed(Identity identity) {
    var control = renewal.requiredState().path("control").path("payload");
    require(
        !contains(control.path("revoked_installation_ids"), identity.identityId()),
        "identity_quarantined");
    var row = row(identity.targetId());
    require(row != null, "identity_quarantined");
    if (row != null) {
      require(
          row.path("isolation")
                  .path("minimum_control_revision")
                  .equals(control.path("control_revision"))
              && text(row.path("isolation"), "control_digest")
                  .equals(digest(renewal.requiredState().path("control"))),
          "identity_quarantined");
      require(
          identity.identityId().equals(text(row, "identity_id"))
              && identity.signingSpki().equals(text(row, "identity_spki")),
          "identity_quarantined");
      require(
          java.util.Set.of("confirmed", "ready").contains(text(row.path("isolation"), "state"))
              && row.has("receipt")
              && !row.path("isolation").path("identity_compromised").asBoolean(),
          "identity_quarantined");
    }
  }

  void mtls(JsonNode request, java.security.cert.X509Certificate certificate) {
    var pin = identity(text(request, "installation_id"));
    require(
        pin.subject()
            .equals(
                certificate
                    .getSubjectX500Principal()
                    .getName(javax.security.auth.x500.X500Principal.RFC2253)),
        "scope_denied");
  }

  public ObjectNode create(JsonNode request) {
    schema("ControlPackage", request, "invalid_chain");
    return renewal.run(
        () -> {
          lock();
          require(
              tenant.toString().equals(text(request, "tenant_id"))
                  && realm.equals(text(request, "realm")),
              "scope_denied");
          var identity = target(text(request, "target_id"));
          require(
              digest(request.path("control")).equals(text(request, "control_digest")),
              "control_rollback");
          renewal.installControl(request.path("control"));
          var control = renewal.requiredState().path("control");
          require(
              control.equals(request.path("control"))
                  && MsgnetV2Verifier.digest(control).equals(text(request, "control_digest")),
              "control_rollback");
          var previous = row(identity.targetId());
          if (previous != null
              && "0".equals(identity.revision())
              && (!identity.identityId().equals(text(previous, "identity_id"))
                  || !identity.signingSpki().equals(text(previous, "identity_spki"))))
            require(
                !identity.identityId().equals(text(previous, "identity_id"))
                    && contains(
                        control.path("payload").path("revoked_installation_ids"),
                        text(previous, "identity_id")),
                "identity_quarantined");
          if (previous != null && !"0".equals(identity.revision())) {
            int order =
                integer(identity.revision())
                    .compareTo(integer(text(previous, "identity_revision")));
            require(order >= 0, "version_rollback");
            if (order == 0)
              require(
                  identity.identityId().equals(text(previous, "identity_id"))
                      && identity.signingSpki().equals(text(previous, "identity_spki")),
                  "same_version_conflict");
            if (order > 0)
              require(
                  !identity.identityId().equals(text(previous, "identity_id"))
                      && contains(
                          control.path("payload").path("revoked_installation_ids"),
                          text(previous, "identity_id")),
                  "identity_quarantined");
          }
          freezeRevokedControls(identity, previous, control);
          if (previous != null
              && previous.path("package").path("control").equals(control)
              && identity.identityId().equals(text(previous, "identity_id"))
              && identity.signingSpki().equals(text(previous, "identity_spki")))
            return (ObjectNode) previous.path("package");
          var pack = controlPackage(identity.targetId(), control);
          var isolation =
              quarantine(pack)
                  .put(
                      "identity_compromised",
                      previous != null
                          && previous.path("isolation").path("identity_compromised").asBoolean()
                          && identity.identityId().equals(text(previous, "identity_id")));
          jdbc.update(
              """
          INSERT INTO collab.msgnet_control_target (id, tenant_id, realm, target_id, identity_id, identity_spki, control_package, isolation_record)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, target_id) DO UPDATE SET
            control_package = EXCLUDED.control_package, isolation_record = EXCLUDED.isolation_record, receipt = NULL,
            identity_id = EXCLUDED.identity_id, identity_spki = EXCLUDED.identity_spki, enforcement_confirmed = false
          """,
              auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
              tenant,
              realm,
              identity.targetId(),
              identity.identityId(),
              identity.signingSpki(),
              MsgnetRenewal.canonical(pack),
              MsgnetRenewal.canonical(isolation));
          jdbc.update(
              "UPDATE collab.msgnet_control_target SET identity_revision = ? WHERE tenant_id = ? AND realm = ? AND target_id = ?",
              new java.math.BigDecimal(identity.revision()),
              tenant,
              realm,
              identity.targetId());
          queue(pack, identity.identityId());
          return pack;
        });
  }

  private ObjectNode controlPackage(String target, JsonNode control) {
    byte[] random = new byte[32];
    new SecureRandom().nextBytes(random);
    var pack =
        MsgnetRenewal.object()
            .put("package_id", UUID.randomUUID().toString())
            .put("tenant_id", tenant.toString())
            .put("realm", realm)
            .put("target_id", target)
            .put("control_digest", digest(control))
            .put("challenge", Base64.getUrlEncoder().withoutPadding().encodeToString(random));
    pack.set("control", control);
    return pack;
  }

  private ObjectNode quarantine(JsonNode pack) {
    var value = MsgnetRenewal.object();
    for (String field :
        java.util.List.of("tenant_id", "realm", "target_id", "control_digest", "challenge"))
      value.set(field, pack.path(field));
    return value
        .put(
            "minimum_control_revision",
            text(pack.path("control").path("payload"), "control_revision"))
        .put("state", "quarantined")
        .put("identity_compromised", false)
        .put("new_chain_ready", false);
  }

  private void queue(JsonNode pack, String identity) {
    jdbc.update(
        "INSERT INTO collab.msgnet_control_queue (id, tenant_id, realm, target_id, identity_id, control_revision, package_id, control_package) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
        auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
        tenant,
        realm,
        text(pack, "target_id"),
        identity,
        new java.math.BigDecimal(text(pack.path("control").path("payload"), "control_revision")),
        text(pack, "package_id"),
        MsgnetRenewal.canonical(pack));
  }

  private void freezeRevokedControls(Target identity, JsonNode previous, JsonNode control) {
    if (!"0".equals(identity.revision())) return;
    // Queue bindings survive a target-row replacement, including replacements made before this
    // recovery path was installed. Only C's recorded target mappings can name former identities.
    var priorIds =
        new java.util.HashSet<>(
            jdbc.queryForList(
                "SELECT DISTINCT identity_id FROM collab.msgnet_control_queue WHERE tenant_id = ? AND realm = ? AND target_id = ?",
                String.class,
                tenant,
                realm,
                identity.targetId()));
    if (previous != null) priorIds.add(text(previous, "identity_id"));
    for (String id : priorIds) {
      if (id.equals(identity.identityId())
          || !contains(control.path("payload").path("revoked_installation_ids"), id)) continue;
      var queued =
          jdbc.queryForList(
              "SELECT control_package FROM collab.msgnet_control_queue WHERE tenant_id = ? AND realm = ? AND target_id = ? AND identity_id = ? AND control_revision = ?",
              String.class,
              tenant,
              realm,
              identity.targetId(),
              id,
              new java.math.BigDecimal(text(control.path("payload"), "control_revision")));
      if (queued.isEmpty()) queue(controlPackage(identity.targetId(), control), id);
      else
        require(
            NetworkJson.parse(queued.getFirst(), 1048576).path("control").equals(control),
            "same_version_conflict");
    }
  }

  private record IsolationBinding(String identityId, ObjectNode isolation) {}

  private java.util.List<IsolationBinding> revokedIsolations(String target, JsonNode control) {
    var result = new java.util.ArrayList<IsolationBinding>();
    var rows =
        jdbc.queryForList(
            "SELECT identity_id, control_package FROM collab.msgnet_control_queue WHERE tenant_id = ? AND realm = ? AND target_id = ? AND control_revision = ? ORDER BY identity_id",
            tenant,
            realm,
            target,
            new java.math.BigDecimal(text(control.path("payload"), "control_revision")));
    for (var row : rows) {
      String id = row.get("identity_id").toString();
      var pack = NetworkJson.parse(row.get("control_package").toString(), 1048576);
      if (!contains(control.path("payload").path("revoked_installation_ids"), id)) continue;
      require(pack.path("control").equals(control), "control_required");
      result.add(new IsolationBinding(id, quarantine(pack)));
    }
    return result;
  }

  private JsonNode enforcement(String identity, JsonNode control) {
    var rows =
        jdbc.queryForList(
            "SELECT isolation_record FROM collab.msgnet_isolation_enforcement WHERE tenant_id = ? AND realm = ? AND identity_id = ? AND control_digest = ?",
            String.class,
            tenant,
            realm,
            identity,
            digest(control));
    return rows.isEmpty() ? null : NetworkJson.parse(rows.getFirst(), 1048576);
  }

  private static boolean sameIsolationBinding(JsonNode left, JsonNode right) {
    for (String field :
        java.util.List.of(
            "tenant_id",
            "realm",
            "target_id",
            "minimum_control_revision",
            "control_digest",
            "challenge")) if (!left.path(field).equals(right.path(field))) return false;
    return true;
  }

  public ObjectNode confirm(JsonNode receipt) {
    require(!nodePins.containsKey(text(receipt, "target_id")), "scope_denied");
    return confirmBound(receipt);
  }

  private ObjectNode confirmBound(JsonNode receipt) {
    return renewal.run(
        () -> {
          lock();
          var row = row(text(receipt, "target_id"));
          require(row != null, "identity_quarantined");
          var identity = target(text(receipt, "target_id"));
          require(
              identity.identityId().equals(text(row, "identity_id"))
                  && identity.signingSpki().equals(text(row, "identity_spki")),
              "identity_quarantined");
          var queued =
              jdbc.queryForList(
                  "SELECT control_package, receipt FROM collab.msgnet_control_queue WHERE tenant_id = ? AND realm = ? AND target_id = ? AND identity_id = ? AND package_id = ? FOR UPDATE",
                  tenant,
                  realm,
                  identity.targetId(),
                  identity.identityId(),
                  text(receipt, "package_id"));
          require(queued.size() == 1, "invalid_transition");
          var item = queued.getFirst();
          var pack = NetworkJson.parse(item.get("control_package").toString(), 1048576);
          boolean used = item.get("receipt") != null;
          boolean replay =
              used && NetworkJson.parse(item.get("receipt").toString(), 1048576).equals(receipt);
          var isolation = (ObjectNode) row.path("isolation").deepCopy();
          try {
            MsgnetPolicy.receipt(
                pack,
                receipt,
                RootKeyPublication.publicKey(identity.signingSpki()),
                identity.identityId(),
                true,
                used,
                replay,
                isolation.path("identity_compromised").asBoolean(),
                isolation.path("new_chain_ready").asBoolean());
          } catch (auriga.common.error.AurigaException e) {
            if (!"collab.msgnet_identity_quarantined".equals(e.errorCode().code())) throw e;
          }
          jdbc.update(
              "UPDATE collab.msgnet_control_queue SET receipt = ? WHERE tenant_id = ? AND realm = ? AND package_id = ?",
              MsgnetRenewal.canonical(receipt),
              tenant,
              realm,
              text(pack, "package_id"));
          if (!pack.equals(row.path("package"))) return isolation;
          if (!isolation.path("identity_compromised").asBoolean())
            isolation.put(
                "state", isolation.path("new_chain_ready").asBoolean() ? "ready" : "confirmed");
          jdbc.update(
              "UPDATE collab.msgnet_control_target SET receipt = ?, isolation_record = ? WHERE tenant_id = ? AND realm = ? AND target_id = ?",
              MsgnetRenewal.canonical(receipt),
              MsgnetRenewal.canonical(isolation),
              tenant,
              realm,
              identity.targetId());
          return isolation;
        });
  }

  public ObjectNode enforce(JsonNode request) {
    schema("IsolationRecord", request, "invalid_chain");
    return renewal.run(
        () -> {
          lock();
          var row = row(text(request, "target_id"));
          require(row != null, "identity_quarantined");
          var stored = row.path("isolation");
          var control = renewal.requiredState().path("control");
          String identityId = text(row, "identity_id");
          boolean historical = !sameIsolationBinding(stored, request);
          if (historical) {
            var binding =
                revokedIsolations(text(request, "target_id"), control).stream()
                    .filter(candidate -> sameIsolationBinding(candidate.isolation(), request))
                    .findFirst()
                    .orElseThrow(() -> failure("invalid_transition"));
            require(
                "quarantined".equals(text(request, "state"))
                    && !request.path("new_chain_ready").asBoolean(),
                "invalid_transition");
            identityId = binding.identityId();
            stored = binding.isolation();
            var prior = enforcement(identityId, control);
            if (prior != null && sameIsolationBinding(prior, stored))
              ((ObjectNode) stored)
                  .put("identity_compromised", prior.path("identity_compromised").asBoolean());
          }
          require(
              stored
                  .path("minimum_control_revision")
                  .equals(control.path("payload").path("control_revision")),
              "control_required");
          require(sameIsolationBinding(stored, request), "invalid_transition");
          boolean compromised =
              stored.path("identity_compromised").asBoolean()
                  || request.path("identity_compromised").asBoolean();
          if (!"quarantined".equals(text(request, "state")))
            require(row.has("receipt") && !compromised, "identity_quarantined");
          if ("ready".equals(text(request, "state")))
            require(request.path("new_chain_ready").asBoolean(), "identity_quarantined");
          var next = (ObjectNode) request.deepCopy();
          next.put("identity_compromised", compromised);
          if (compromised) next.put("state", "quarantined").put("new_chain_ready", false);
          if (!historical)
            jdbc.update(
                "UPDATE collab.msgnet_control_target SET isolation_record = ?, enforcement_confirmed = ? WHERE tenant_id = ? AND realm = ? AND target_id = ?",
                MsgnetRenewal.canonical(next),
                "quarantined".equals(text(next, "state")),
                tenant,
                realm,
                text(request, "target_id"));
          if ("quarantined".equals(text(next, "state")))
            jdbc.update(
                "INSERT INTO collab.msgnet_isolation_enforcement (id, tenant_id, realm, identity_id, control_digest, isolation_record) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, identity_id, control_digest) DO UPDATE SET isolation_record = EXCLUDED.isolation_record",
                auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
                tenant,
                realm,
                identityId,
                text(next, "control_digest"),
                MsgnetRenewal.canonical(next));
          else
            jdbc.update(
                "DELETE FROM collab.msgnet_isolation_enforcement WHERE tenant_id = ? AND realm = ? AND identity_id = ? AND control_digest = ?",
                tenant,
                realm,
                identityId,
                text(next, "control_digest"));
          return next;
        });
  }

  public boolean permitted(
      String callerTenant, String callerRealm, String identityId, String targetId) {
    if (!tenant.toString().equals(callerTenant) || !realm.equals(callerRealm)) return false;
    try {
      return renewal.run(
          () -> {
            var identity = identity(identityId);
            require(identity.targetId().equals(targetId), "scope_denied");
            requireUsable(identity);
            return true;
          });
    } catch (auriga.common.error.AurigaException
        | org.springframework.dao.DataAccessException unavailable) {
      return false;
    }
  }

  public boolean projectionPermitted(
      String callerTenant, String callerRealm, String identityId, String targetId) {
    if (!tenant.toString().equals(callerTenant) || !realm.equals(callerRealm)) return false;
    try {
      return renewal.run(
          () -> {
            var identity = identity(identityId);
            require(identity.targetId().equals(targetId), "scope_denied");
            requireProjectionAllowed(identity);
            return true;
          });
    } catch (auriga.common.error.AurigaException
        | org.springframework.dao.DataAccessException unavailable) {
      return false;
    }
  }

  public boolean claim(auriga.contracts.msgnet.model.ProjectionRequest request) {
    var value = MsgnetNetworkRenewal.tree(request);
    schema("ProjectionRequest", value, "invalid_chain");
    return renewal.run(
        () -> {
          lock();
          var identity = identity(text(value, "installation_id"));
          require(
              tenant.toString().equals(text(value, "tenant_id"))
                  && realm.equals(text(value, "realm")),
              "scope_denied");
          requireProjectionAllowed(identity);
          MsgnetPolicy.identitySignature(
              value,
              "Auriga.NetworkProjectionRequest.v2",
              RootKeyPublication.publicKey(identity.signingSpki()));
          require(
              identity.identityId().equals(text(value.path("identity_auth"), "kid"))
                  && identity.component().equals(text(value, "component"))
                  && identity.gatewayId().equals(text(value, "gateway_id")),
              "scope_denied");
          return jdbc.update(
                  "INSERT INTO collab.msgnet_installation_nonce (id, tenant_id, realm, identity_id, nonce) VALUES (?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, identity_id, nonce) DO NOTHING",
                  auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
                  tenant,
                  realm,
                  identity.identityId(),
                  text(value, "nonce"))
              == 1;
        });
  }

  private JsonNode node(String authenticatedNodeId) {
    return nodePins.values().stream()
        .filter(pin -> authenticatedNodeId.equals(text(pin, "node_id")))
        .findFirst()
        .orElseThrow(() -> failure("scope_denied"));
  }

  public JsonNode pendingNode(String authenticatedNodeId) {
    return pending(text(node(authenticatedNodeId), "target_id"));
  }

  public void requireNode(String authenticatedNodeId) {
    node(authenticatedNodeId);
  }

  public JsonNode confirmNode(String authenticatedNodeId, JsonNode receipt) {
    return confirmNode(
        authenticatedNodeId, text(node(authenticatedNodeId), "identity_revision"), receipt);
  }

  public JsonNode confirmNode(String authenticatedNodeId, String revision, JsonNode receipt) {
    return renewal.run(
        () -> {
          var pin = nodePins.get(text(receipt, "target_id"));
          require(pin != null, "scope_denied");
          var record = row(text(receipt, "target_id"));
          require(record != null, "identity_quarantined");
          MsgnetPolicy.nodeControlBinding(
              pin,
              authenticatedNodeId,
              text(receipt, "target_id"),
              text(receipt.path("identity_auth"), "kid"),
              revision,
              record.path("isolation").path("identity_compromised").asBoolean(),
              false,
              true);
          return confirmBound(receipt);
        });
  }

  public JsonNode isolation(String target) {
    return renewal.run(
        () -> {
          target(target);
          var record = row(target);
          if (record == null)
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.NOT_FOUND);
          var value = record.path("isolation");
          var control = renewal.requiredState().path("control");
          require(
              value
                      .path("minimum_control_revision")
                      .equals(control.path("payload").path("control_revision"))
                  && text(value, "control_digest").equals(digest(control)),
              "identity_quarantined");
          // The trusted enforcer must first report the old target's exact current-control
          // challenge. A newly installed pin and its own quarantine do not acknowledge it.
          for (var binding : revokedIsolations(target, control)) {
            var fact = enforcement(binding.identityId(), control);
            if (fact == null
                || !sameIsolationBinding(fact, binding.isolation())
                || !"quarantined".equals(text(fact, "state"))) return binding.isolation();
          }
          return value;
        });
  }

  public JsonNode pending(String target) {
    return renewal.run(
        () -> {
          target(target);
          var identity = target(target);
          var rows =
              jdbc.queryForList(
                  "SELECT control_package FROM collab.msgnet_control_queue WHERE tenant_id = ? AND realm = ? AND target_id = ? AND identity_id = ? AND receipt IS NULL ORDER BY control_revision LIMIT 1",
                  String.class,
                  tenant,
                  realm,
                  target,
                  identity.identityId());
          if (rows.isEmpty())
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.NOT_FOUND);
          return NetworkJson.parse(rows.getFirst(), 1048576);
        });
  }

  public void refreshTargets() {
    var targets = new java.util.HashSet<>(identities.keySet());
    targets.addAll(nodePins.keySet());
    for (String target : targets) {
      var control = NetworkJson.parse(renewal.publicControl(), 1048576);
      var request =
          MsgnetRenewal.object()
              .put("package_id", "install")
              .put("tenant_id", tenant.toString())
              .put("realm", realm)
              .put("target_id", target)
              .put(
                  "challenge", Base64.getUrlEncoder().withoutPadding().encodeToString(new byte[32]))
              .put("control_digest", digest(control));
      request.set("control", control);
      create(request);
    }
  }

  void mtlsReader(java.security.cert.X509Certificate certificate) {
    String subject =
        certificate
            .getSubjectX500Principal()
            .getName(javax.security.auth.x500.X500Principal.RFC2253);
    require(
        identities.values().stream().anyMatch(pin -> pin.subject().equals(subject)),
        "scope_denied");
  }

  private ObjectNode row(String target) {
    var rows =
        jdbc.query(
            "SELECT control_package, receipt, isolation_record, identity_id, identity_spki, identity_revision FROM collab.msgnet_control_target WHERE tenant_id = ? AND realm = ? AND target_id = ?",
            (rs, n) -> {
              var value = MsgnetRenewal.object();
              value.set("package", NetworkJson.parse(rs.getString(1), 1048576));
              if (rs.getString(2) != null)
                value.set("receipt", NetworkJson.parse(rs.getString(2), 1048576));
              value.set("isolation", NetworkJson.parse(rs.getString(3), 1048576));
              value
                  .put("identity_id", rs.getString(4))
                  .put("identity_spki", rs.getString(5))
                  .put("identity_revision", rs.getBigDecimal(6).toPlainString());
              return value;
            },
            tenant,
            realm,
            target);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private void lock() {
    jdbc.queryForObject(
        "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
        Object.class,
        tenant + ":network-keys:" + realm);
  }
}
