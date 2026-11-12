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

  public DesktopReleases(Path root, String pem, Clock clock, ResourceAccess access)
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
  }

  public ObjectNode verify(ObjectNode envelope) {
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
              "notes");
      if (!body.propertyNames().equals(fields)
          || body.path("schema").asInt() != 1
          || !text(body, "product", 32).equals("Auriga Desktop")
          || !text(body, "channel", 16).equals("stable")
          || !text(body, "platform", 16).equals("win-x64")
          || !text(body, "packageId", 200).matches("[A-Za-z0-9.+-]{1,200}")
          || !text(body, "sha256", 64).matches("[a-f0-9]{64}")
          || counter(body, "sizeBytes") > MAX_BYTES
          || counter(body, "sequence") < 1
          || !ResourceSemver.valid(text(body, "version", 128))
          || !ResourceSemver.valid(text(body, "minimumVersion", 128))
          || !body.path("notes").isString()
          || body.path("notes").asString().length() > 8192) throw invalid();
      Instant published = Instant.parse(text(body, "publishedAt", 64)),
          expiry = Instant.parse(text(body, "notAfter", 64));
      if (published.isAfter(clock.instant())
          || !expiry.isAfter(clock.instant())
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
    return store(actor.tenant()).read();
  }

  public ObjectNode publish(AurigaPrincipal principal, ObjectNode envelope, InputStream input) {
    var actor = access.administrator(principal, MANAGE);
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
      return store.change(
          state -> {
            access.administrator(principal, MANAGE);
            verify(envelope);
            long seq = body.path("sequence").asLong();
            if (seq <= state.path("highestSequence").asLong()) throw new AurigaException(CONFLICT);
            var items = state.withArray("items");
            for (var item : items)
              if (ResourceSemver.compare(
                      body.path("version").asString(),
                      item.path("manifest").path("version").asString())
                  <= 0) throw new AurigaException(CONFLICT);
            try {
              Files.move(
                  stage,
                  store.root().resolve(body.path("sha256").asString() + ".zip"),
                  StandardCopyOption.ATOMIC_MOVE);
            } catch (IOException e) {
              throw new AurigaException(UNAVAILABLE);
            }
            ObjectNode item = JSON.createObjectNode();
            item.set("manifest", envelope);
            item.put("withdrawn", false);
            items.add(item);
            state.put("highestSequence", seq);
            audit(state, principal, seq, "publish");
            return envelope;
          });
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
    store(actor.tenant())
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
  }

  private void audit(ObjectNode state, AurigaPrincipal actor, long seq, String action) {
    state
        .withArray("audit")
        .add(
            JSON.createObjectNode()
                .put("actor", actor.subject())
                .put("sequence", seq)
                .put("action", action)
                .put("at", clock.instant().toString()));
  }

  public ObjectNode latest(AurigaPrincipal principal) {
    var actor = access.member(principal);
    ObjectNode best = null;
    for (var item : store(actor.tenant()).read().withArray("items")) {
      if (item.path("withdrawn").asBoolean()) continue;
      var manifest = (ObjectNode) item.path("manifest");
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

  public record Download(InputStream stream, long size) {}

  public Download download(AurigaPrincipal principal, long sequence) {
    var actor = access.member(principal);
    var store = store(actor.tenant());
    return store.change(
        state -> {
          for (var item : state.withArray("items"))
            if (!item.path("withdrawn").asBoolean()
                && item.path("manifest").path("sequence").asLong() == sequence) {
              var body = verify((ObjectNode) item.path("manifest"));
              try {
                var stream =
                    Files.newInputStream(
                        store.root().resolve(body.path("sha256").asString() + ".zip"),
                        LinkOption.NOFOLLOW_LINKS);
                return new Download(stream, body.path("sizeBytes").asLong());
              } catch (IOException e) {
                throw new AurigaException(UNAVAILABLE);
              }
            }
          throw new AurigaException(NOT_FOUND);
        });
  }
}
