package auriga.workspace.app;

import static auriga.workspace.domain.WorkspaceErrors.valid;
import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.*;
import auriga.workspace.domain.*;
import auriga.workspace.persistence.WorkspaceStore;
import com.google.protobuf.ByteString;
import com.google.protobuf.Timestamp;
import java.security.MessageDigest;
import java.security.cert.X509Certificate;
import java.time.Clock;
import java.util.*;

public final class NodeEnrollment {
  public final WorkspaceStore store;
  public final NodeCa ca;
  public final Clock clock;
  private final String endpoint;
  private final List<ArtifactSigningKey> artifactKeys;

  public NodeEnrollment(
      WorkspaceStore store,
      NodeCa ca,
      Clock clock,
      String endpoint,
      List<ArtifactSigningKey> artifactKeys) {
    this.store = store;
    this.ca = ca;
    this.clock = clock;
    this.endpoint = endpoint;
    this.artifactKeys = List.copyOf(artifactKeys);
    valid(endpoint.startsWith("https://"));
  }

  public record Token(
      UUID id,
      String name,
      String token,
      String caSha256,
      String command,
      UUID createdBy,
      java.time.Instant createdAt,
      java.time.Instant expiresAt) {
    @Override
    public String toString() {
      return "JoinToken[id=" + id + ", credentials=redacted]";
    }
  }

  public Token createToken(String name, int minutes, UUID actor) {
    valid(minutes >= 5 && minutes <= 1440 && (name == null || name.length() <= 64));
    UUID id = UuidV7.next(clock), tenant = store.tenant();
    store.register(tenant, "tenant", tenant);
    store.register(id, "token", tenant);
    String secret =
        Base64.getUrlEncoder().withoutPadding().encodeToString(WorkspaceCrypto.random(32));
    var now = clock.instant();
    var expires = now.plusSeconds(minutes * 60L);
    store.tx(
        () ->
            store.jdbc.update(
                "INSERT INTO join_token(id,tenant_id,name,secret_hash,expires_at,created_by,created_at) VALUES(?,?,?,?,?,?,?)",
                id,
                tenant,
                name,
                WorkspaceCrypto.hash(secret),
                time(expires),
                actor,
                time(now)));
    String token = id + "." + secret;
    return new Token(
        id,
        name,
        token,
        ca.fingerprint(),
        "auriga-agent join --server "
            + endpoint
            + " --token "
            + token
            + " --ca-sha256 "
            + ca.fingerprint(),
        actor,
        now,
        expires);
  }

  public JoinResponse join(JoinRequest request) {
    UUID tokenId;
    String secret;
    try {
      String[] parts = request.getToken().split("\\.", -1);
      if (parts.length != 2 || parts[1].length() != 43) throw new IllegalArgumentException();
      tokenId = UUID.fromString(parts[0]);
      secret = parts[1];
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    }
    UUID tenant = store.route(tokenId, "token");
    UUID node = UuidV7.next(clock);
    valid(
        request.getSealingPublicKey().size() == 32
            && !request.getSealingPublicKey().equals(ByteString.copyFrom(new byte[32])));
    valid(
        org.bouncycastle.math.ec.rfc7748.X25519.calculateAgreement(
            WorkspaceCrypto.random(32),
            0,
            request.getSealingPublicKey().toByteArray(),
            0,
            new byte[32],
            0));
    valid(
        Set.of("linux", "windows").contains(request.getOs())
            && Set.of("amd64", "arm64").contains(request.getArch())
            && request.getHostname().length() <= 255
            && request.getAgentVersion().length() <= 128);
    JoinResponse response =
        store.in(
            tenant,
            () -> {
              var tokens =
                  store.jdbc.queryForList(
                      "SELECT * FROM join_token WHERE tenant_id=? AND id=? FOR UPDATE",
                      tenant,
                      tokenId);
              if (tokens.isEmpty()) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
              var token = tokens.getFirst();
              if (token.get("used_at") != null
                  || !instant(token, "expires_at").isAfter(clock.instant())
                  || !MessageDigest.isEqual(
                      (byte[]) token.get("secret_hash"), WorkspaceCrypto.hash(secret)))
                throw new AurigaException(CommonErrors.UNAUTHENTICATED);
              X509Certificate leaf = ca.issue(request.getCsr().toByteArray(), node);
              store.register(node, "node", tenant);
              store.jdbc.update(
                  "UPDATE join_token SET used_at=? WHERE tenant_id=? AND id=?",
                  time(clock.instant()),
                  tenant,
                  tokenId);
              store.jdbc.update(
                  "INSERT INTO node(id,tenant_id,name,hostname,os,arch,agent_version,sealing_key,certificate_serial,certificate_expires_at,joined_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
                  node,
                  tenant,
                  request.getHostname(),
                  request.getHostname(),
                  request.getOs(),
                  request.getArch(),
                  request.getAgentVersion(),
                  request.getSealingPublicKey().toByteArray(),
                  leaf.getSerialNumber().toString(),
                  time(leaf.getNotAfter().toInstant()),
                  time(clock.instant()));
              return JoinResponse.newBuilder()
                  .setNodeId(node.toString())
                  .setCertificateChain(ByteString.copyFrom(ca.chain(leaf)))
                  .setCaBundle(ByteString.copyFrom(ca.bundle()))
                  .setControlEndpoint(endpoint)
                  .setCertificateExpiresAt(timestamp(leaf.getNotAfter().toInstant()))
                  .addAllArtifactSigningKeys(artifactKeys)
                  .build();
            });
    return response;
  }

  public UUID identity(X509Certificate leaf) {
    ca.verify(leaf);
    try {
      var sans = leaf.getSubjectAlternativeNames();
      if (sans == null || sans.size() != 1) throw new IllegalArgumentException();
      var san = sans.iterator().next();
      String uri = san.get(1).toString();
      if (!Integer.valueOf(6).equals(san.get(0)) || !uri.startsWith("auriga://node/"))
        throw new IllegalArgumentException();
      UUID node = UUID.fromString(uri.substring("auriga://node/".length()));
      UUID tenant = store.route(node, "node");
      store.in(
          tenant,
          () -> {
            var row = store.node(node);
            if ((boolean) row.get("revoked")
                || !row.get("certificate_serial").equals(leaf.getSerialNumber().toString()))
              throw new AurigaException(CommonErrors.UNAUTHENTICATED);
            return null;
          });
      return node;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    } catch (Exception e) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    }
  }

  public RenewCertificateResponse renew(
      UUID node, X509Certificate current, RenewCertificateRequest request) {
    identity(current);
    valid(request.getSealingPublicKey().isEmpty() || request.getSealingPublicKey().size() == 32);
    if (!request.getSealingPublicKey().isEmpty())
      valid(!request.getSealingPublicKey().equals(ByteString.copyFrom(new byte[32])));
    if (!request.getSealingPublicKey().isEmpty())
      valid(
          org.bouncycastle.math.ec.rfc7748.X25519.calculateAgreement(
              WorkspaceCrypto.random(32),
              0,
              request.getSealingPublicKey().toByteArray(),
              0,
              new byte[32],
              0));
    var leaf = ca.issue(request.getCsr().toByteArray(), node);
    return store.in(
        store.route(node, "node"),
        () -> {
          var row = store.node(node);
          if ((boolean) row.get("revoked")
              || !row.get("certificate_serial").equals(current.getSerialNumber().toString()))
            throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          store.jdbc.update(
              "UPDATE node SET certificate_serial=?,certificate_expires_at=?,sealing_key=? WHERE tenant_id=? AND id=?",
              leaf.getSerialNumber().toString(),
              time(leaf.getNotAfter().toInstant()),
              request.getSealingPublicKey().isEmpty()
                  ? row.get("sealing_key")
                  : request.getSealingPublicKey().toByteArray(),
              store.tenant(),
              node);
          return RenewCertificateResponse.newBuilder()
              .setCertificateChain(ByteString.copyFrom(ca.chain(leaf)))
              .setCertificateExpiresAt(timestamp(leaf.getNotAfter().toInstant()))
              .addAllArtifactSigningKeys(artifactKeys)
              .build();
        });
  }

  public static Timestamp timestamp(java.time.Instant time) {
    return Timestamp.newBuilder()
        .setSeconds(time.getEpochSecond())
        .setNanos(time.getNano())
        .build();
  }
}
