package auriga.iam;

import java.io.IOException;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.concurrent.TimeUnit;

/** Checks existing Windows ACLs without changing them; POSIX files are created owner-only. */
final class NetworkSourceJavaPrivateFiles {
  private NetworkSourceJavaPrivateFiles() {}

  static void requirePrivate(Path directory) throws IOException {
    if (!Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(directory))
      throw new IOException("Host credential root is not a plain directory");
    var posix =
        Files.getFileAttributeView(
            directory, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
    if (posix != null) {
      if (!posix
          .readAttributes()
          .permissions()
          .equals(PosixFilePermissions.fromString("rwx------")))
        throw new IOException("Host credential root is not private");
      return;
    }
    var acl =
        Files.getFileAttributeView(
            directory, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
    if (acl == null) throw new IOException("Cannot verify host credential root privacy");
    // Java's Windows principal lookup accepts account names, not SID strings. .NET exposes
    // language-independent SIDs directly. This command only reads the existing descriptor.
    String script =
        """
        $ErrorActionPreference='Stop'
        $a=Get-Acl -LiteralPath $env:AURIGA_BRIDGE_PRIVATE_ROOT
        $ok=@([Security.Principal.WindowsIdentity]::GetCurrent().User.Value,
          'S-1-5-18','S-1-5-32-544','S-1-3-0')
        $owner=([Security.Principal.NTAccount]::new($a.Owner)).Translate(
          [Security.Principal.SecurityIdentifier]).Value
        if ($owner -notin $ok) { exit 2 }
        foreach ($r in $a.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
          if ($r.AccessControlType -eq 'Allow' -and
              ($r.FileSystemRights -band [Security.AccessControl.FileSystemRights]::ReadData) -ne 0 -and
              $r.IdentityReference.Value -notin $ok) { exit 2 }
        }
        exit 0
        """;
    var builder =
        new ProcessBuilder("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script);
    builder.environment().put("AURIGA_BRIDGE_PRIVATE_ROOT", directory.toAbsolutePath().toString());
    builder.redirectOutput(ProcessBuilder.Redirect.DISCARD);
    builder.redirectError(ProcessBuilder.Redirect.DISCARD);
    var process = builder.start();
    try {
      if (!process.waitFor(10, TimeUnit.SECONDS))
        throw new IOException("Cannot verify host credential root privacy");
      if (process.exitValue() == 2)
        throw new IOException("Host credential root ACL allows other readers");
      if (process.exitValue() != 0)
        throw new IOException("Cannot verify host credential root privacy");
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new IOException("Interrupted while checking host credential root privacy", e);
    } finally {
      if (process.isAlive()) {
        process.destroyForcibly();
        try {
          process.onExit().get(5, TimeUnit.SECONDS);
        } catch (Exception cleanup) {
          throw new IOException("Private-root ACL reader survived cleanup", cleanup);
        }
      }
    }
  }

  static void replace(Path path, String content) throws IOException {
    requirePrivate(path.getParent());
    Path temporary =
        Files.getFileAttributeView(path.getParent(), PosixFileAttributeView.class) == null
            ? Files.createTempFile(path.getParent(), ".credential-", ".tmp")
            : Files.createTempFile(
                path.getParent(),
                ".credential-",
                ".tmp",
                PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rw-------")));
    try {
      Files.writeString(temporary, content, StandardOpenOption.TRUNCATE_EXISTING);
      Files.move(
          temporary, path, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
    } finally {
      Files.deleteIfExists(temporary);
    }
  }
}
