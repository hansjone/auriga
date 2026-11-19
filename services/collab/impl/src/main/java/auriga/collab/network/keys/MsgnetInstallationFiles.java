package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.*;
import java.security.MessageDigest;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Bounded local installation files. No path or native error is returned on failure. */
final class MsgnetInstallationFiles {
  private MsgnetInstallationFiles() {}

  static IllegalArgumentException invalid() {
    return new IllegalArgumentException("Invalid protected msgnet installation input");
  }

  static void check(boolean condition) {
    if (!condition) throw invalid();
  }

  static void fields(JsonNode value, Set<String> required, Set<String> optional) {
    check(value.isObject());
    var actual = new HashSet<>(value.propertyNames());
    check(actual.containsAll(required));
    actual.removeAll(required);
    check(optional.containsAll(actual));
  }

  static String string(JsonNode value, String name) {
    var field = value.path(name);
    check(field.isString() && !field.asString().isBlank() && field.asString().length() <= 4096);
    return field.asString();
  }

  static String identifier(JsonNode value, String name) {
    String result = string(value, name);
    check(result.matches("[A-Za-z0-9._-]{1,128}"));
    return result;
  }

  static void version(JsonNode value) {
    check(value.path("version").isIntegralNumber() && value.path("version").asLong() == 1);
  }

  static Path path(String text) {
    check(!text.startsWith("\\\\") && !text.startsWith("//"));
    Path result = Path.of(text);
    check(result.isAbsolute() && result.normalize().equals(result));
    return result;
  }

  static byte[] read(Path file, boolean secret, int limit) {
    ProtectedKeyFiles.check(file, secret);
    try {
      check(Files.size(file) <= limit);
      if (Files.getFileStore(file).supportsFileAttributeView("unix"))
        check(
            ((Number) Files.getAttribute(file, "unix:nlink", LinkOption.NOFOLLOW_LINKS)).longValue()
                == 1);
      return ProtectedKeyFiles.read(file, limit);
    } catch (IOException failure) {
      throw invalid();
    }
  }

  static JsonNode json(Path file, boolean secret) {
    byte[] bytes = read(file, secret, 1048576);
    try {
      return decodeJson(bytes);
    } finally {
      Arrays.fill(bytes, (byte) 0);
    }
  }

  private static JsonNode decodeJson(byte[] bytes) {
    try {
      // The existing strict UTF-8/duplicate/unknown-number parser remains authoritative.
      return NetworkJson.parse(
          StandardCharsets.UTF_8
              .newDecoder()
              .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
              .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
              .decode(java.nio.ByteBuffer.wrap(bytes))
              .toString(),
          1048576);
    } catch (java.nio.charset.CharacterCodingException failure) {
      throw invalid();
    }
  }

  static String hash(byte[] bytes) {
    try {
      return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
    } catch (java.security.GeneralSecurityException failure) {
      throw invalid();
    }
  }

  static Path referencePath(JsonNode ref) {
    fields(ref, Set.of("path", "sha256"), Set.of());
    check(string(ref, "sha256").matches("[0-9a-f]{64}"));
    return path(string(ref, "path"));
  }

  static byte[] referenced(JsonNode ref, boolean secret, int limit) {
    byte[] bytes = read(referencePath(ref), secret, limit);
    if (!hash(bytes).equals(string(ref, "sha256"))) {
      Arrays.fill(bytes, (byte) 0);
      throw invalid();
    }
    return bytes;
  }

  static JsonNode referencedJson(JsonNode ref, boolean secret) {
    byte[] bytes = referenced(ref, secret, 1048576);
    try {
      return decodeJson(bytes);
    } finally {
      Arrays.fill(bytes, (byte) 0);
    }
  }

  static ObjectNode reference(Path path) {
    byte[] bytes = read(path, false, 1048576);
    try {
      return MsgnetRenewal.object().put("path", path.toString()).put("sha256", hash(bytes));
    } finally {
      Arrays.fill(bytes, (byte) 0);
    }
  }

  static Path newDirectory(String text) throws IOException {
    Path target = path(text);
    check(
        target.getParent() != null
            && Files.isDirectory(target.getParent(), LinkOption.NOFOLLOW_LINKS));
    for (Path p = target; p != null; p = p.getParent()) check(!Files.isSymbolicLink(p));
    if (Files.getFileAttributeView(target.getParent(), PosixFileAttributeView.class) != null)
      Files.createDirectory(
          target,
          PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
    else {
      Files.createDirectory(target);
      ownerOnly(target);
    }
    return target;
  }

  static void ownerOnly(Path target) throws IOException {
    if (Files.getFileAttributeView(target, PosixFileAttributeView.class) != null) {
      Files.setPosixFilePermissions(
          target,
          PosixFilePermissions.fromString(Files.isDirectory(target) ? "rwx------" : "rw-------"));
      return;
    }
    var acl =
        Files.getFileAttributeView(target, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
    check(acl != null);
    var entry =
        AclEntry.newBuilder()
            .setType(AclEntryType.ALLOW)
            .setPrincipal(acl.getOwner())
            .setPermissions(Set.of(AclEntryPermission.values()));
    if (Files.isDirectory(target))
      entry.setFlags(AclEntryFlag.FILE_INHERIT, AclEntryFlag.DIRECTORY_INHERIT);
    acl.setAcl(List.of(entry.build()));
  }

  static Path write(Path directory, String name, byte[] bytes) throws IOException {
    Path file = directory.resolve(name);
    check(file.getParent().equals(directory) && !name.contains("/") && !name.contains("\\"));
    if (Files.getFileAttributeView(directory, PosixFileAttributeView.class) != null) {
      try (var channel =
          java.nio.channels.FileChannel.open(
              file,
              Set.of(
                  StandardOpenOption.CREATE_NEW,
                  StandardOpenOption.WRITE,
                  LinkOption.NOFOLLOW_LINKS),
              PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rw-------")))) {
        var buffer = java.nio.ByteBuffer.wrap(bytes);
        while (buffer.hasRemaining()) channel.write(buffer);
        channel.force(true);
      }
    } else {
      Files.write(file, bytes, StandardOpenOption.CREATE_NEW, StandardOpenOption.WRITE);
      ownerOnly(file);
    }
    return file;
  }

  static Path writeJson(Path directory, String name, JsonNode value) throws IOException {
    return write(
        directory, name, NetworkJson.canonical(value, 1048576).getBytes(StandardCharsets.UTF_8));
  }
}
