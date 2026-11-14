package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.workspace.persistence.WorkspaceStore;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.time.Clock;
import java.util.*;
import tools.jackson.databind.json.JsonMapper;

/** W signs only canonical OpenTicket claims, never protojson or browser supplied identity. */
public final class SharedDshTickets {
  public interface Entry {
    /** Trusted deployment mapping, including the IP instance prefix when needed. */
    URI exchangeUrl(SharedDshBinding binding);

    /** Live E replay authority; unavailable must throw, never return false as fallback. */
    boolean consumed(UUID jti);
  }

  public record Open(SharedDshBinding binding, String ticket, URI exchangeUrl, long expiresAt) {
    @Override
    public String toString() {
      return "Open[redacted]";
    }
  }

  private final WorkspaceStore store;
  private final SharedDshPlacements placements;
  private final Entry entry;
  private final Clock clock;
  private final String kid;
  private final PrivateKey key;

  public SharedDshTickets(
      WorkspaceStore store,
      SharedDshPlacements placements,
      Entry entry,
      Clock clock,
      String kid,
      PrivateKey key) {
    valid(kid != null && !kid.isBlank() && kid.length() <= 128);
    valid(key != null && Set.of("EdDSA", "Ed25519").contains(key.getAlgorithm()));
    this.store = store;
    this.placements = placements;
    this.entry = entry;
    this.clock = clock;
    this.kid = kid;
    this.key = key;
  }

  public Open open(UUID member, UUID session, String idempotencyKey) {
    valid(idempotencyKey != null && !idempotencyKey.isBlank() && idempotencyKey.length() <= 128);
    var allocated = placements.allocate(member, session);
    // A pending allocation was durably created, but no ticket may be issued yet.
    conflict(allocated.state().equals("ready"));
    return store.tx(
        () -> {
          store.lock();
          var resolved =
              placements.resolve(
                  member, UUID.fromString(allocated.binding().getPlacementId()), session);
          long now = clock.instant().getEpochSecond();
          var old =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_open WHERE tenant_id=? AND member_id=? AND idempotency_key=?",
                  store.tenant(),
                  member,
                  idempotencyKey);
          UUID jti;
          long issued;
          if (!old.isEmpty()
              && ((Number) old.getFirst().get("created_at")).longValue() + 86400 > now) {
            var row = old.getFirst();
            conflict(
                row.get("session_id").equals(session)
                    && Arrays.equals(
                        (byte[]) row.get("binding"), resolved.binding().toByteArray()));
            jti = (UUID) row.get("jti");
            issued = ((Number) row.get("created_at")).longValue();
            conflict(now >= issued && now < issued + 60 && !entry.consumed(jti));
          } else {
            store.jdbc.update(
                "DELETE FROM shared_dsh_open WHERE tenant_id=? AND member_id=? AND idempotency_key=?",
                store.tenant(),
                member,
                idempotencyKey);
            jti = UuidV7.next();
            issued = now;
            store.jdbc.update(
                "INSERT INTO shared_dsh_open(tenant_id,member_id,idempotency_key,session_id,jti,binding,created_at) VALUES(?,?,?,?,?,?,?)",
                store.tenant(),
                member,
                idempotencyKey,
                session,
                jti,
                resolved.binding().toByteArray(),
                issued);
          }
          var b = resolved.binding();
          var binding = new TreeMap<String, Object>();
          binding.put("tenantId", b.getTenantId());
          binding.put("memberId", b.getMemberId());
          binding.put("placementId", b.getPlacementId());
          binding.put("physicalInstanceId", b.getPhysicalInstanceId());
          binding.put("mode", mode(b.getMode()));
          binding.put("epoch", b.getEpoch());
          binding.put("revision", b.getRevision());
          var claims = new TreeMap<String, Object>();
          claims.put("schemaVersion", 1);
          claims.put("binding", binding);
          claims.put("platformSessionId", session.toString());
          claims.put("audience", "auriga.placement.exchange");
          claims.put("issuedAt", issued);
          claims.put("expiresAt", issued + 60);
          claims.put("jti", jti.toString());
          URI url = entry.exchangeUrl(b);
          valid(
              url != null
                  && "https".equals(url.getScheme())
                  && url.getHost() != null
                  && url.getUserInfo() == null
                  && url.getQuery() == null
                  && url.getFragment() == null
                  && url.getPath().endsWith("/_auriga/placement-ticket"));
          return new Open(b, sign(kid, claims, key), url, issued + 60);
        });
  }

  public static String sign(String kid, SortedMap<String, Object> claims, PrivateKey key) {
    try {
      var json = JsonMapper.builder().build();
      var header = new TreeMap<String, Object>();
      header.put("alg", "EdDSA");
      header.put("kid", kid);
      header.put("typ", "auriga-shared-ticket+jwt");
      var encoder = Base64.getUrlEncoder().withoutPadding();
      String input =
          encoder.encodeToString(json.writeValueAsBytes(header))
              + "."
              + encoder.encodeToString(json.writeValueAsBytes(claims));
      var signer = Signature.getInstance("Ed25519");
      signer.initSign(key);
      signer.update(input.getBytes(StandardCharsets.US_ASCII));
      return input + "." + encoder.encodeToString(signer.sign());
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }
}
