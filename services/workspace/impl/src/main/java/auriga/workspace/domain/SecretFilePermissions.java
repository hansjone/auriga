package auriga.workspace.domain;

import java.io.IOException;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.*;

/** Fail closed on shared private keys; owners, SYSTEM and local administrators are trusted. */
public final class SecretFilePermissions {
  private SecretFilePermissions() {}

  public static boolean exposedPosix(Set<PosixFilePermission> permissions) {
    return permissions.stream()
        .anyMatch(p -> p.name().startsWith("GROUP_") || p.name().startsWith("OTHERS_"));
  }

  public static void check(Path path) throws IOException {
    if (!Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS))
      throw new IOException("CA private key must be a regular, non-symlink file");
    var posix =
        Files.getFileAttributeView(path, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
    if (posix != null) {
      if (exposedPosix(posix.readAttributes().permissions()))
        throw new IOException("CA private key has group or other permissions");
      return;
    }
    var acl =
        Files.getFileAttributeView(path, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
    if (acl == null)
      throw new IOException("Cannot verify CA private key permissions on this filesystem");
    Set<UserPrincipal> trusted = new HashSet<>();
    trusted.add(acl.getOwner());
    var lookup = path.getFileSystem().getUserPrincipalLookupService();
    for (String name :
        List.of("NT AUTHORITY\\SYSTEM", "BUILTIN\\Administrators", "S-1-5-18", "S-1-5-32-544"))
      try {
        trusted.add(lookup.lookupPrincipalByName(name));
      } catch (IOException ignored) {
        /* Unknown localized principal remains untrusted. */
      }
    checkAcl(acl.getAcl(), trusted);
  }

  public static void checkAcl(List<AclEntry> entries, Set<UserPrincipal> trusted)
      throws IOException {
    Set<AclEntryPermission> sensitive =
        EnumSet.of(
            AclEntryPermission.READ_DATA,
            AclEntryPermission.WRITE_DATA,
            AclEntryPermission.APPEND_DATA,
            AclEntryPermission.DELETE,
            AclEntryPermission.WRITE_ACL,
            AclEntryPermission.WRITE_OWNER);
    for (var entry : entries)
      if (entry.type() == AclEntryType.ALLOW
          && !trusted.contains(entry.principal())
          && entry.permissions().stream().anyMatch(sensitive::contains))
        throw new IOException("CA private key ACL permits an untrusted principal");
  }
}
