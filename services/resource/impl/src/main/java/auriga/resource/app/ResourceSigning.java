package auriga.resource.app;

import auriga.common.error.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.*;
import java.security.spec.PKCS8EncodedKeySpec;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Integer-only RFC8785 domain of resource/signatures.schema.json. Never uses protobuf JSON. */
public final class ResourceSigning {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private final String releaseKid, revocationKid;
  private final PrivateKey release, revocation;

  public ResourceSigning(
      String releaseKid, PrivateKey release, String revocationKid, PrivateKey revocation) {
    if (releaseKid == null
        || revocationKid == null
        || !releaseKid.matches("[A-Za-z0-9._-]{1,128}")
        || !revocationKid.matches("[A-Za-z0-9._-]{1,128}")
        || releaseKid.equals(revocationKid)
        || release == null
        || revocation == null
        || Arrays.equals(release.getEncoded(), revocation.getEncoded()))
      throw new IllegalArgumentException("Independent resource signing purposes required");
    this.releaseKid = releaseKid;
    this.revocationKid = revocationKid;
    this.release = release;
    this.revocation = revocation;
  }

  public ObjectNode sign(ObjectNode body, boolean revoke) {
    ObjectNode unsigned = body.deepCopy();
    unsigned.remove("signature");
    try {
      Signature signer = Signature.getInstance("Ed25519");
      signer.initSign(revoke ? revocation : release);
      signer.update(canonical(unsigned).getBytes(StandardCharsets.UTF_8));
      ObjectNode out = unsigned.deepCopy();
      out.set(
          "signature",
          JSON.createObjectNode()
              .put("alg", "Ed25519")
              .put("kid", revoke ? revocationKid : releaseKid)
              .put("value", Base64.getUrlEncoder().withoutPadding().encodeToString(signer.sign())));
      return out;
    } catch (GeneralSecurityException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  public static String canonical(JsonNode node) {
    if (node.isObject()) {
      var names = new ArrayList<>(node.propertyNames());
      names.sort(String::compareTo);
      var items = new ArrayList<String>();
      for (String n : names) items.add(quote(n) + ":" + canonical(node.get(n)));
      return "{" + String.join(",", items) + "}";
    }
    if (node.isArray()) {
      var items = new ArrayList<String>();
      node.forEach(n -> items.add(canonical(n)));
      return "[" + String.join(",", items) + "]";
    }
    if (node.isString()) return quote(node.asString());
    if (node.isIntegralNumber()
        && node.canConvertToLong()
        && Math.abs(node.asLong()) <= 9007199254740991L
        && node.asLong() != Long.MIN_VALUE) return Long.toString(node.asLong());
    if (node.isBoolean() || node.isNull()) return node.toString();
    throw new IllegalArgumentException("Outside integer JCS domain");
  }

  public static String time(java.time.Instant value) {
    return java.time.format.DateTimeFormatter.ISO_INSTANT
        .format(value)
        .replaceFirst("(\\.\\d*?[1-9])0+Z$", "$1Z")
        .replaceFirst("\\.0+Z$", "Z");
  }

  private static String quote(String text) {
    for (int i = 0; i < text.length(); i++) {
      char c = text.charAt(i);
      if (Character.isHighSurrogate(c)) {
        if (++i >= text.length() || !Character.isLowSurrogate(text.charAt(i)))
          throw new IllegalArgumentException("Invalid Unicode");
      } else if (Character.isLowSurrogate(c)) throw new IllegalArgumentException("Invalid Unicode");
    }
    String encoded = JSON.writeValueAsString(text);
    var result = new StringBuilder();
    for (int i = 0; i < encoded.length(); i++) {
      char c = encoded.charAt(i);
      result.append(c);
      if (c == '\\' && i + 1 < encoded.length()) {
        char escaped = encoded.charAt(++i);
        result.append(escaped);
        if (escaped == 'u') {
          result.append(encoded.substring(i + 1, i + 5).toLowerCase(Locale.ROOT));
          i += 4;
        }
      }
    }
    return result.toString();
  }

  public static PrivateKey load(Path path) {
    try {
      Path absolute = path.toAbsolutePath().normalize();
      for (Path p = absolute; p != null; p = p.getParent())
        if (Files.isSymbolicLink(p)) throw new IllegalArgumentException("Unsafe key path");
      if (!absolute.toRealPath().equals(absolute))
        throw new IllegalArgumentException("Indirect key path");
      if (!Files.isRegularFile(absolute, LinkOption.NOFOLLOW_LINKS))
        throw new IllegalArgumentException("Missing key");
      var before =
          Files.readAttributes(absolute, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
      var parent = absolute.getParent();
      var parentPosix =
          Files.getFileAttributeView(
              parent, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
      if (parentPosix != null) {
        var directory = parentPosix.readAttributes();
        var serviceOwner =
            parent
                .getFileSystem()
                .getUserPrincipalLookupService()
                .lookupPrincipalByName(System.getProperty("user.name"));
        if (!directory.owner().equals(serviceOwner)
            || !directory.permissions().equals(PosixFilePermissions.fromString("rwx------")))
          throw new IllegalArgumentException("Unsafe key directory owner or permissions");
        var permissions = directory.permissions();
        if (permissions.contains(PosixFilePermission.GROUP_WRITE)
            || permissions.contains(PosixFilePermission.OTHERS_WRITE))
          throw new IllegalArgumentException("Writable key directory");
      }
      var posix =
          Files.getFileAttributeView(
              absolute, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
      if (posix != null) {
        if (!posix
            .readAttributes()
            .owner()
            .equals(
                absolute
                    .getFileSystem()
                    .getUserPrincipalLookupService()
                    .lookupPrincipalByName(System.getProperty("user.name"))))
          throw new IllegalArgumentException("Invalid key owner");
        if (posix.readAttributes().permissions().stream()
            .anyMatch(p -> p.name().startsWith("GROUP_") || p.name().startsWith("OTHERS_")))
          throw new IllegalArgumentException("Exposed key");
      } else {
        var acl =
            Files.getFileAttributeView(
                absolute, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
        if (acl == null) throw new IllegalArgumentException("Unsupported key protection");
        Set<UserPrincipal> trusted = new HashSet<>();
        trusted.add(acl.getOwner());
        for (String account : List.of("NT AUTHORITY\\SYSTEM", "BUILTIN\\Administrators"))
          try {
            trusted.add(
                absolute
                    .getFileSystem()
                    .getUserPrincipalLookupService()
                    .lookupPrincipalByName(account));
          } catch (UserPrincipalNotFoundException e) {
            /* unrecognized principals remain untrusted */
          }
        for (var e : acl.getAcl())
          if (e.type() == AclEntryType.ALLOW
              && !trusted.contains(e.principal())
              && e.permissions().stream()
                  .anyMatch(
                      p ->
                          Set.of(
                                  AclEntryPermission.READ_DATA,
                                  AclEntryPermission.WRITE_DATA,
                                  AclEntryPermission.APPEND_DATA,
                                  AclEntryPermission.DELETE,
                                  AclEntryPermission.WRITE_ACL,
                                  AclEntryPermission.WRITE_OWNER)
                              .contains(p))) throw new IllegalArgumentException("Exposed key");
        var directoryAcl =
            Files.getFileAttributeView(
                parent, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
        if (directoryAcl == null)
          throw new IllegalArgumentException("Unsupported key directory protection");
        for (var e : directoryAcl.getAcl())
          if (e.type() == AclEntryType.ALLOW
              && !trusted.contains(e.principal())
              && e.permissions().stream()
                  .anyMatch(
                      p ->
                          Set.of(
                                  AclEntryPermission.WRITE_DATA,
                                  AclEntryPermission.APPEND_DATA,
                                  AclEntryPermission.DELETE_CHILD,
                                  AclEntryPermission.DELETE,
                                  AclEntryPermission.WRITE_ACL,
                                  AclEntryPermission.WRITE_OWNER)
                              .contains(p)))
            throw new IllegalArgumentException("Writable key directory");
      }
      byte[] bytes;
      try (var in = Files.newInputStream(absolute, LinkOption.NOFOLLOW_LINKS)) {
        bytes = in.readNBytes(4097);
      }
      try {
        var after =
            Files.readAttributes(absolute, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
        if (bytes.length > 4096
            || !Objects.equals(before.fileKey(), after.fileKey())
            || before.size() != after.size()
            || !before.lastModifiedTime().equals(after.lastModifiedTime()))
          throw new IllegalArgumentException("Invalid key");
        return KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(bytes));
      } finally {
        Arrays.fill(bytes, (byte) 0);
      }
    } catch (java.io.IOException | GeneralSecurityException e) {
      throw new IllegalArgumentException("Cannot load protected resource key");
    }
  }
}
