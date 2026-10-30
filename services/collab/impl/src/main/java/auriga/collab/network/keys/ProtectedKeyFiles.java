package auriga.collab.network.keys;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.AclFileAttributeView;
import java.nio.file.attribute.PosixFileAttributeView;
import java.nio.file.attribute.PosixFilePermission;
import java.util.HashSet;
import java.util.Set;

/** Refuses symlinks, unbounded input, exposed secrets and writable trust/config files. */
final class ProtectedKeyFiles {
  private ProtectedKeyFiles() {}

  static byte[] readSecret(Path path) {
    check(path, true);
    return read(path, 4096);
  }

  static String readText(Path path) {
    check(path, false);
    // NetworkJson performs strict UTF-8 decoding on all JSON inputs.
    return auriga.collab.app.NetworkJson.canonical(
        auriga.collab.app.NetworkJson.parse(read(path, 65536)));
  }

  static byte[] read(Path path, int maximum) {
    try {
      if (!Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS)) throw invalid();
      try (var channel =
              java.nio.channels.FileChannel.open(
                  path, StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS);
          var input = java.nio.channels.Channels.newInputStream(channel)) {
        byte[] bytes = input.readNBytes(maximum + 1);
        if (bytes.length > maximum) throw invalid();
        return bytes;
      }
    } catch (IOException e) {
      throw invalid();
    }
  }

  static void check(Path path, boolean secret) {
    Path absolute = path.toAbsolutePath().normalize();
    try {
      for (Path current = absolute; current != null; current = current.getParent()) {
        if (Files.isSymbolicLink(current)) throw invalid();
      }
      if (!Files.exists(absolute, LinkOption.NOFOLLOW_LINKS)) throw invalid();
      var posix =
          Files.getFileAttributeView(
              absolute, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
      if (posix != null) {
        var permissions = posix.readAttributes().permissions();
        for (var p : permissions) {
          if (p == PosixFilePermission.GROUP_WRITE
              || p == PosixFilePermission.OTHERS_WRITE
              || (secret && (p.name().startsWith("GROUP_") || p.name().startsWith("OTHERS_"))))
            throw invalid();
        }
        return;
      }
      var acl =
          Files.getFileAttributeView(
              absolute, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
      if (acl == null) throw invalid();
      Set<java.nio.file.attribute.UserPrincipal> trusted = new HashSet<>();
      trusted.add(acl.getOwner());
      var lookup = absolute.getFileSystem().getUserPrincipalLookupService();
      // JDK's Windows lookup accepts account names, not raw SID strings. If a localized
      // installation cannot resolve these well-known accounts, require owner-only ACLs.
      for (String account : new String[] {"NT AUTHORITY\\SYSTEM", "BUILTIN\\Administrators"}) {
        try {
          trusted.add(lookup.lookupPrincipalByName(account));
        } catch (java.nio.file.attribute.UserPrincipalNotFoundException e) {
          // Fail closed for unrecognized principals below; never broaden to Users/Everyone.
        }
      }
      Set<AclEntryPermission> dangerous =
          new HashSet<>(
              Set.of(
                  AclEntryPermission.WRITE_DATA,
                  AclEntryPermission.APPEND_DATA,
                  AclEntryPermission.DELETE,
                  AclEntryPermission.DELETE_CHILD,
                  AclEntryPermission.WRITE_ACL,
                  AclEntryPermission.WRITE_OWNER));
      if (secret) dangerous.add(AclEntryPermission.READ_DATA);
      for (var entry : acl.getAcl()) {
        if (entry.type() == AclEntryType.ALLOW
            && !trusted.contains(entry.principal())
            && entry.permissions().stream().anyMatch(dangerous::contains)) throw invalid();
      }
    } catch (IOException e) {
      throw invalid();
    }
  }

  static IllegalArgumentException invalid() {
    return new IllegalArgumentException(
        "Network key file is unavailable or insufficiently protected");
  }
}
