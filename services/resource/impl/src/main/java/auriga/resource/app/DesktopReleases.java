package auriga.resource.app;

import static auriga.common.error.CommonErrors.*;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.AurigaException;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.persistence.DesktopStore;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.security.spec.X509EncodedKeySpec;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Independent desktop release ledger and immutable blobs. No online signing keys. */
public final class DesktopReleases {
  public static final String MANAGE = "resource.versions.publish";
  public static final long MAX_BYTES = 4294967296L;
  private final Path root;
  private final PublicKey key;
  private final Clock clock;
  private final ResourceAccess access;
  private final UUID publisherTenant;
  private final java.util.function.Consumer<ObjectNode> unifiedAudit;

  public DesktopReleases(
      Path root,
      String pem,
      Clock clock,
      ResourceAccess access,
      UUID publisherTenant,
      java.util.function.Consumer<ObjectNode> unifiedAudit)
      throws Exception {
    this.root = root.toAbsolutePath().normalize();
    Files.createDirectories(this.root);
    for (Path p = this.root; p != null; p = p.getParent())
      if (Files.isSymbolicLink(p)) throw new IOException("Unsafe desktop directory");
    this.key =
        KeyFactory.getInstance("Ed25519")
            .generatePublic(
                new X509EncodedKeySpec(
                    Base64.getMimeDecoder()
                        .decode(
                            pem.replace("-----BEGIN PUBLIC KEY-----", "")
                                .replace("-----END PUBLIC KEY-----", ""))));
    this.clock = clock;
    this.access = access;
    this.publisherTenant = Objects.requireNonNull(publisherTenant);
    this.unifiedAudit = Objects.requireNonNull(unifiedAudit);
  }

  public ObjectNode verify(ObjectNode envelope) {
    return verify(envelope, false);
  }

  private ObjectNode verify(ObjectNode envelope, boolean allowExpired) {
    try {
      var body = envelope.deepCopy();
      var sig = body.remove("signature");
      if (sig == null
          || sig.size() != 3
          || !"Ed25519".equals(sig.path("alg").asString())
          || !"auriga-desktop-release-1".equals(sig.path("kid").asString())
          || !sig.path("value").asString().matches("[A-Za-z0-9_-]{86}")) throw invalid();
      Signature verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(key);
      verifier.update(ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8));
      if (!verifier.verify(Base64.getUrlDecoder().decode(sig.path("value").asString())))
        throw invalid();
      var fields =
          new HashSet<>(
              Set.of(
                  "schema",
                  "product",
                  "version",
                  "packageId",
                  "channel",
                  "platform",
                  "sizeBytes",
                  "sha256",
                  "minimumVersion",
                  "publishedAt",
                  "notAfter",
                  "sequence",
                  "notes",
                  "components"));
      int schema = body.path("schema").asInt();
      if (schema == 2) fields.addAll(Set.of("rolloutPercentage", "rollbackFrom"));
      if (!body.propertyNames().equals(fields)
          || (!body.path("schema").isIntegralNumber()
              || (schema != 1 && schema != 2)
              || body.path("schema").asLong() != schema)
          || !text(body, "product", 32).equals("Auriga Desktop")
          || !Set.of("stable", "beta").contains(text(body, "channel", 16))
          || (schema == 1 && !text(body, "channel", 16).equals("stable"))
          || (schema == 2
              && (!body.path("rolloutPercentage").isIntegralNumber()
                  || !body.path("rolloutPercentage").canConvertToInt()
                  || body.path("rolloutPercentage").asInt() < 0
                  || body.path("rolloutPercentage").asInt() > 100
                  || (!body.path("rollbackFrom").isNull()
                      && !ResourceSemver.valid(text(body, "rollbackFrom", 128)))))
          || !text(body, "platform", 16).equals("win-x64")
          || !text(body, "packageId", 200).matches("[A-Za-z0-9.+-]{1,200}")
          || !text(body, "sha256", 64).matches("[a-f0-9]{64}")
          || counter(body, "sizeBytes") < 1
          || counter(body, "sizeBytes") > MAX_BYTES
          || counter(body, "sequence") < 1
          || !ResourceSemver.valid(text(body, "version", 128))
          || !ResourceSemver.valid(text(body, "minimumVersion", 128))
          || !body.path("notes").isString()
          || body.path("notes").asString().length() > 8192) throw invalid();
      if (ResourceSemver.compare(
              body.path("minimumVersion").asString(), body.path("version").asString())
          > 0) throw invalid();
      var components = body.path("components");
      if (!components.isObject()
          || !components
              .propertyNames()
              .equals(Set.of("dshVersion", "nodeVersion", "pluginVersion"))) throw invalid();
      for (String field : components.propertyNames())
        if (!ResourceSemver.valid(text(components, field, 128))) throw invalid();
      Instant published = Instant.parse(text(body, "publishedAt", 64)),
          expiry = Instant.parse(text(body, "notAfter", 64));
      if (published.isAfter(clock.instant())
          || (!allowExpired && !expiry.isAfter(clock.instant()))
          || !expiry.isAfter(published)) throw invalid();
      return body;
    } catch (Exception e) {
      throw invalid();
    }
  }

  private DesktopStore store(UUID tenant) {
    try {
      return new DesktopStore(root.resolve(tenant.toString()));
    } catch (IOException e) {
      throw new AurigaException(UNAVAILABLE);
    }
  }

  public ObjectNode list(AurigaPrincipal principal) {
    var actor = access.administrator(principal, MANAGE);
    publisher(actor.tenant());
    return store(publisherTenant).read();
  }

  public ObjectNode publish(AurigaPrincipal principal, ObjectNode envelope, InputStream input) {
    var actor = access.administrator(principal, MANAGE);
    publisher(actor.tenant());
    var body = verify(envelope);
    var store = store(actor.tenant());
    Path temporary = null;
    try (input) {
      temporary = Files.createTempFile(store.root(), ".upload-", ".partial");
      MessageDigest hash = MessageDigest.getInstance("SHA-256");
      long count = 0;
      try (var output = Files.newOutputStream(temporary)) {
        byte[] buffer = new byte[1048576];
        for (int n; (n = input.read(buffer)) != -1; ) {
          count += n;
          if (count > body.path("sizeBytes").asLong()) throw invalid();
          hash.update(buffer, 0, n);
          output.write(buffer, 0, n);
        }
      }
      if (count != body.path("sizeBytes").asLong()
          || !HexFormat.of().formatHex(hash.digest()).equals(body.path("sha256").asString()))
        throw invalid();
      Path stage = temporary;
      var result =
          store.change(
              state -> {
                access.administrator(principal, MANAGE);
                verify(envelope);
                long seq = body.path("sequence").asLong();
                if (seq <= state.path("highestSequence").asLong())
                  throw new AurigaException(CONFLICT);
                var items = state.withArray("items");
                tools.jackson.databind.JsonNode previous = null;
                for (var item : items) {
                  var candidate = item.path("manifest");
                  if (candidate.path("channel").equals(body.path("channel"))
                      && (previous == null
                          || candidate.path("sequence").asLong()
                              > previous.path("sequence").asLong())) previous = candidate;
                }
                boolean rollback =
                    !body.path("rollbackFrom").isMissingNode()
                        && !body.path("rollbackFrom").isNull();
                if (rollback
                    && (previous == null
                        || !body.path("rollbackFrom").equals(previous.path("version"))))
                  throw new AurigaException(CONFLICT);
                if (previous != null) {
                  int comparison =
                      ResourceSemver.compare(
                          body.path("version").asString(), previous.path("version").asString());
                  if (comparison < 0 && !rollback) throw new AurigaException(CONFLICT);
                  if (rollback && comparison >= 0) throw new AurigaException(CONFLICT);
                  if (comparison == 0) {
                    for (String field : Set.of("packageId", "sha256", "sizeBytes", "components"))
                      if (!body.path(field).equals(previous.path(field)))
                        throw new AurigaException(CONFLICT);
                  }
                }
                try {
                  Path blob = store.root().resolve(body.path("sha256").asString() + ".exe");
                  if (Files.exists(blob)) Files.delete(stage);
                  else Files.move(stage, blob, StandardCopyOption.ATOMIC_MOVE);
                } catch (IOException e) {
                  throw new AurigaException(UNAVAILABLE);
                }
                ObjectNode item = JSON.createObjectNode();
                item.set("manifest", envelope);
                item.put("withdrawn", false);
                items.add(item);
                state.put("highestSequence", seq);
                audit(state, principal, seq, rollback ? "rollback" : "publish");
                return envelope;
              });
      flushAudit();
      return result;
    } catch (IOException | GeneralSecurityException e) {
      throw new AurigaException(UNAVAILABLE);
    } finally {
      if (temporary != null)
        try {
          Files.deleteIfExists(temporary);
        } catch (IOException ignored) {
        }
    }
  }

  public void withdraw(AurigaPrincipal principal, long sequence) {
    var actor = access.administrator(principal, MANAGE);
    publisher(actor.tenant());
    store(publisherTenant)
        .change(
            state -> {
              for (var item : state.withArray("items"))
                if (item.path("manifest").path("sequence").asLong() == sequence) {
                  ((ObjectNode) item).put("withdrawn", true);
                  audit(state, principal, sequence, "withdraw");
                  return true;
                }
              throw new AurigaException(NOT_FOUND);
            });
    flushAudit();
  }

  private void audit(ObjectNode state, AurigaPrincipal actor, long seq, String action) {
    state
        .withArray("audit")
        .add(
            JSON.createObjectNode()
                .put("id", auriga.common.persistence.UuidV7.next(clock).toString())
                .put("actor", actor.subject())
                .put("sequence", seq)
                .put("action", action)
                .put("at", clock.instant().toString()));
  }

  private void publisher(UUID tenant) {
    if (!publisherTenant.equals(tenant)) throw new AurigaException(FORBIDDEN);
  }

  @org.springframework.scheduling.annotation.Scheduled(fixedDelay = 30000)
  public void flushAudit() {
    try {
      store(publisherTenant)
          .change(
              state -> {
                for (var event : state.withArray("audit"))
                  if (!event.has("id"))
                    ((ObjectNode) event)
                        .put("id", auriga.common.persistence.UuidV7.next(clock).toString());
                return true;
              });
      store(publisherTenant)
          .change(
              state -> {
                for (var event : state.withArray("audit")) {
                  if (event.path("exported").asBoolean()) continue;
                  if (!event.has("id")) continue;
                  unifiedAudit.accept((ObjectNode) event);
                  ((ObjectNode) event).put("exported", true);
                }
                return true;
              });
    } catch (RuntimeException e) {
      org.slf4j.LoggerFactory.getLogger(DesktopReleases.class)
          .warn("Desktop audit export pending", e);
    }
  }

  public ObjectNode latest() {
    return latest("stable");
  }

  public ObjectNode latest(String channel) {
    if (!Set.of("stable", "beta").contains(channel)) throw invalid();
    ObjectNode best = null;
    for (var item : store(publisherTenant).read().withArray("items")) {
      if (item.path("withdrawn").asBoolean()) continue;
      var manifest = (ObjectNode) item.path("manifest");
      if (!channel.equals(manifest.path("channel").asString())) continue;
      if (!Files.isRegularFile(
          store(publisherTenant).root().resolve(manifest.path("sha256").asString() + ".exe"),
          LinkOption.NOFOLLOW_LINKS)) continue;
      try {
        verify(manifest);
      } catch (AurigaException e) {
        continue;
      }
      if (best == null || manifest.path("sequence").asLong() > best.path("sequence").asLong())
        best = manifest;
    }
    if (best == null) throw new AurigaException(NOT_FOUND);
    return best;
  }

  public record Download(InputStream stream, long size, String filename) {}

  public Download download(long sequence) {
    var store = store(publisherTenant);
    return store.change(
        state -> {
          for (var item : state.withArray("items"))
            if (!item.path("withdrawn").asBoolean()
                && item.path("manifest").path("sequence").asLong() == sequence) {
              var body = verify((ObjectNode) item.path("manifest"));
              try {
                var stream =
                    Files.newInputStream(
                        store.root().resolve(body.path("sha256").asString() + ".exe"),
                        LinkOption.NOFOLLOW_LINKS);
                return new Download(
                    stream,
                    body.path("sizeBytes").asLong(),
                    "AurigaDSH-Setup-" + body.path("version").asString() + ".exe");
              } catch (IOException e) {
                throw new AurigaException(UNAVAILABLE);
              }
            }
          throw new AurigaException(NOT_FOUND);
        });
  }
}
