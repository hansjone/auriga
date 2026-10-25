package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkSigningKeys;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.security.KeyFactory;
import java.security.spec.PKCS8EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.Arrays;
import java.util.HashSet;
import java.util.Map;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Loads only manifest-listed business keys; roots stay verification-only in the online process. */
final class SigningKeyLoader {
  private SigningKeyLoader() {}

  static NetworkSigningKeys load(
      JsonNode payload,
      Path state,
      Map<String, String> secrets,
      Clock clock,
      NetworkSigningKeys.SigningGuard guard) {
    ProtectedKeyFiles.check(state, false);
    ProtectedKeyFiles.check(state.toAbsolutePath().getParent(), false);
    var keys =
        new NetworkSigningKeys(
            clock,
            1,
            state,
            guard,
            java.time.Duration.between(
                    Instant.parse(payload.path("issued_at").asString()),
                    Instant.parse(payload.path("valid_until").asString()))
                .getSeconds());
    try {
      var required = new HashSet<String>();
      var purposes = new HashSet<String>();
      for (var entry : payload.path("keys")) {
        var status =
            NetworkSigningKeys.State.valueOf(
                entry.path("state").asString().toUpperCase(java.util.Locale.ROOT));
        String kid = entry.path("kid").asString();
        java.security.PrivateKey secret = null;
        if (status == NetworkSigningKeys.State.ACTIVE || status == NetworkSigningKeys.State.NEXT) {
          required.add(kid);
          if (!secrets.containsKey(kid)) throw ProtectedKeyFiles.invalid();
          byte[] bytes = ProtectedKeyFiles.readSecret(Path.of(secrets.get(kid)));
          try {
            secret =
                KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(bytes));
          } finally {
            Arrays.fill(bytes, (byte) 0);
          }
        }
        purposes.add(entry.path("purpose").asString());
        keys.add(
            payload.path("realm").asString(),
            entry.path("purpose").asString(),
            kid,
            RootKeyPublication.publicKey(entry.path("public_key_spki").asString()),
            secret,
            Instant.parse(entry.path("not_before").asString()),
            Instant.parse(entry.path("not_after").asString()),
            status);
      }
      if (!required.equals(secrets.keySet())
          || !purposes.equals(
              java.util.Set.of("business_delivery", "protocol_response", "management_state"))
          || !NetworkJson.canonical(payload)
              .equals(keys.publicKeysJson(payload.path("realm").asString())))
        throw ProtectedKeyFiles.invalid();
      return keys;
    } catch (RuntimeException | java.security.GeneralSecurityException e) {
      keys.close();
      throw new IllegalStateException("Cannot initialize approved network signing keys");
    }
  }

  /** Offline installation while the signer is stopped; never rewrites a locked live state. */
  static void installState(JsonNode payload, Path state) throws java.io.IOException {
    Path file = state.toAbsolutePath().normalize();
    ProtectedKeyFiles.check(file.getParent(), false);
    Path lockFile = file.resolveSibling(file.getFileName() + ".lock");
    if (Files.exists(lockFile)) ProtectedKeyFiles.check(lockFile, false);
    try (var channel =
            FileChannel.open(
                lockFile,
                StandardOpenOption.CREATE,
                StandardOpenOption.WRITE,
                java.nio.file.LinkOption.NOFOLLOW_LINKS);
        var lock = channel.tryLock()) {
      if (lock == null) throw ProtectedKeyFiles.invalid();
      if (Files.exists(file)) {
        ProtectedKeyFiles.check(file, false);
        var old = NetworkJson.parse(ProtectedKeyFiles.readText(file));
        var version = new java.math.BigInteger(old.path("version").asString());
        if (version.compareTo(new java.math.BigInteger(payload.path("keyset_version").asString()))
            >= 0) throw RootKeyPublication.invalid();
      }
      var snapshot =
          JsonMapper.builder()
              .build()
              .createObjectNode()
              .put("version", payload.path("keyset_version").asString())
              .put("issued_at", payload.path("issued_at").asString());
      var array = snapshot.putArray("keys");
      for (var key : payload.path("keys")) {
        array
            .addObject()
            .put("kid", key.path("kid").asString())
            .put("realm", payload.path("realm").asString())
            .put("purpose", key.path("purpose").asString())
            .put("state", key.path("state").asString().toUpperCase(java.util.Locale.ROOT))
            .put("not_before", key.path("not_before").asString())
            .put("not_after", key.path("not_after").asString())
            .put("public_key_spki", key.path("public_key_spki").asString());
      }
      Path temporary = Files.createTempFile(file.getParent(), "key-state-", ".tmp");
      try {
        try (var output = FileChannel.open(temporary, StandardOpenOption.WRITE)) {
          ByteBuffer buffer =
              java.nio.charset.StandardCharsets.UTF_8.encode(NetworkJson.canonical(snapshot));
          while (buffer.hasRemaining()) output.write(buffer);
          output.force(true);
        }
        Files.move(
            temporary, file, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
      } finally {
        Files.deleteIfExists(temporary);
      }
    }
  }
}
