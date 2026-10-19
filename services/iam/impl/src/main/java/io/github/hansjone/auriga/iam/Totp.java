package io.github.hansjone.auriga.iam;

import java.nio.ByteBuffer;
import java.security.*;
import java.time.Instant;
import java.util.*;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/** RFC 6238 SHA-1/6 digits/30s. Last accepted counter is persisted under a row lock. */
public final class Totp {
  private static final String BASE32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

  public static String generate() {
    byte[] raw = new byte[20];
    new SecureRandom().nextBytes(raw);
    StringBuilder s = new StringBuilder();
    int buffer = 0, bits = 0;
    for (byte b : raw) {
      buffer = buffer << 8 | (b & 255);
      bits += 8;
      while (bits >= 5) {
        bits -= 5;
        s.append(BASE32.charAt(buffer >> bits & 31));
      }
    }
    return s.toString();
  }

  static byte[] decode(String secret) {
    var out = new java.io.ByteArrayOutputStream();
    int buffer = 0, bits = 0;
    for (char c : secret.toUpperCase(Locale.ROOT).toCharArray()) {
      int v = BASE32.indexOf(c);
      if (v < 0) throw new IllegalArgumentException("Invalid Base32");
      buffer = buffer << 5 | v;
      bits += 5;
      if (bits >= 8) {
        bits -= 8;
        out.write(buffer >> bits & 255);
      }
    }
    return out.toByteArray();
  }

  public static String code(String secret, long counter) {
    try {
      Mac mac = Mac.getInstance("HmacSHA1");
      mac.init(new SecretKeySpec(decode(secret), "HmacSHA1"));
      byte[] hash = mac.doFinal(ByteBuffer.allocate(8).putLong(counter).array());
      int offset = hash[hash.length - 1] & 15;
      int binary = ByteBuffer.wrap(hash, offset, 4).getInt() & 0x7fffffff;
      return String.format(Locale.ROOT, "%06d", binary % 1000000);
    } catch (GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  static long verify(String secret, String supplied, Instant now, long last) {
    if (!supplied.matches("[0-9]{6}")) return -1;
    long current = now.getEpochSecond() / 30;
    for (long c = current - 1; c <= current + 1; c++)
      if (c > last && Secrets.same(code(secret, c), supplied)) return c;
    return -1;
  }

  private Totp() {}
}
