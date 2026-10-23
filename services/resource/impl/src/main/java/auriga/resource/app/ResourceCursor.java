package auriga.resource.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.io.*;
import java.security.MessageDigest;
import java.time.Clock;
import java.util.Base64;
import java.util.UUID;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/** Authenticated opaque keyset cursors, scoped to caller, tenant, list and requested limit. */
public final class ResourceCursor {
  private final byte[] key;
  private final Clock clock;
  private static final Base64.Encoder ENCODE = Base64.getUrlEncoder().withoutPadding();
  private static final Base64.Decoder DECODE = Base64.getUrlDecoder();

  public ResourceCursor(byte[] key, Clock clock) {
    if (key.length < 32)
      throw new IllegalArgumentException("Resource cursor key must have at least 32 bytes");
    this.key = key.clone();
    this.clock = clock;
  }

  public record Position(UUID after, UUID ceiling, long expires) {}

  public Position initial(UUID ceiling) {
    return new Position(new UUID(0, 0), ceiling, clock.instant().plusSeconds(900).getEpochSecond());
  }

  public String encode(ResourceAccess.Reader reader, String scope, int limit, Position position) {
    try {
      var bytes = new ByteArrayOutputStream();
      try (var out = new DataOutputStream(bytes)) {
        out.writeInt(1);
        out.writeUTF(reader.tenant().toString());
        out.writeUTF(reader.account().toString());
        out.writeUTF(scope);
        out.writeInt(limit);
        out.writeUTF(position.after().toString());
        out.writeUTF(position.ceiling().toString());
        out.writeLong(position.expires());
      }
      byte[] payload = bytes.toByteArray();
      return ENCODE.encodeToString(payload) + "." + ENCODE.encodeToString(sign(payload));
    } catch (IOException e) {
      throw new IllegalStateException(e);
    }
  }

  public Position decode(String cursor, ResourceAccess.Reader reader, String scope, int limit) {
    if (cursor == null || cursor.isEmpty()) return null;
    try {
      if (cursor.length() > 2048) throw new IllegalArgumentException();
      var parts = cursor.split("\\.", -1);
      if (parts.length != 2) throw new IllegalArgumentException();
      byte[] payload = DECODE.decode(parts[0]);
      byte[] signature = DECODE.decode(parts[1]);
      if (!ENCODE.encodeToString(payload).equals(parts[0])
          || !ENCODE.encodeToString(signature).equals(parts[1])
          || !MessageDigest.isEqual(signature, sign(payload))) throw new IllegalArgumentException();
      try (var in = new DataInputStream(new ByteArrayInputStream(payload))) {
        if (in.readInt() != 1
            || !in.readUTF().equals(reader.tenant().toString())
            || !in.readUTF().equals(reader.account().toString())
            || !in.readUTF().equals(scope)
            || in.readInt() != limit) throw new IllegalArgumentException();
        var position =
            new Position(
                UUID.fromString(in.readUTF()), UUID.fromString(in.readUTF()), in.readLong());
        if (in.available() != 0 || position.expires() <= clock.instant().getEpochSecond())
          throw new IllegalArgumentException();
        return position;
      }
    } catch (IOException | IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  private byte[] sign(byte[] payload) {
    try {
      var mac = Mac.getInstance("HmacSHA256");
      mac.init(new SecretKeySpec(key, "HmacSHA256"));
      return mac.doFinal(payload);
    } catch (java.security.GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }
}
