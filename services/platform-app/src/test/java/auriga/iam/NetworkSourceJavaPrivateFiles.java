package auriga.iam;

import java.io.IOException;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.HashSet;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

/** Creates only this test's root with private permissions; checks never change existing ACLs. */
final class NetworkSourceJavaPrivateFiles {
  private NetworkSourceJavaPrivateFiles() {}

  static Path createDirectory() throws IOException {
    if (FileSystems.getDefault().supportedFileAttributeViews().contains("posix"))
      return Files.createTempDirectory(
          "auriga-java-bridge-",
          PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rwx------")));
    var directory =
        Path.of(System.getProperty("java.io.tmpdir"))
            .resolve("auriga-java-bridge-" + UUID.randomUUID())
            .toAbsolutePath();
    // Supply the descriptor at creation: an inherited descriptor can deny WRITE_DAC even
    // on a newly created directory. Never change any pre-existing directory's permissions.
    windows(
        directory,
        """
        $directory=[IO.DirectoryInfo]::new($env:AURIGA_BRIDGE_PRIVATE_ROOT)
        if ($directory.Exists) { throw 'Test root already exists' }
        $user=[Security.Principal.WindowsIdentity]::GetCurrent().User
        $acl=[Security.AccessControl.DirectorySecurity]::new()
        $acl.SetAccessRuleProtection($true,$false)
        $rule=[Security.AccessControl.FileSystemAccessRule]::new($user,
          [Security.AccessControl.FileSystemRights]::FullControl,
          [Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit',
          [Security.AccessControl.PropagationFlags]::None,
          [Security.AccessControl.AccessControlType]::Allow)
        $acl.AddAccessRule($rule)
        $directory.Create($acl)
        """);
    try {
      requirePrivate(directory);
      return directory;
    } catch (IOException failure) {
      try {
        Files.deleteIfExists(directory);
      } catch (IOException cleanup) {
        failure.addSuppressed(cleanup);
      }
      throw failure;
    }
  }

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
    // Use .NET directly: Windows PowerShell 5.1 may inherit pwsh 7's incompatible module path.
    // No Get-Acl/Set-Acl, Import-Module, profile or localized account-name lookup is needed.
    windows(
        directory,
        """
        $a=([IO.DirectoryInfo]::new($env:AURIGA_BRIDGE_PRIVATE_ROOT)).GetAccessControl()
        $ok=@([Security.Principal.WindowsIdentity]::GetCurrent().User.Value,
          'S-1-5-18','S-1-5-32-544')
        $owner=$a.GetOwner([Security.Principal.SecurityIdentifier]).Value
        if (-not $a.AreAccessRulesProtected -or $owner -notin $ok) { exit 2 }
        foreach ($r in $a.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) {
          if ($r.AccessControlType -eq 'Allow' -and
              ($r.FileSystemRights -band [Security.AccessControl.FileSystemRights]::ReadData) -ne 0 -and
              $r.IdentityReference.Value -notin $ok) { exit 2 }
        }
        exit 0
        """);
  }

  private static void windows(Path directory, String script) throws IOException {
    var builder =
        new ProcessBuilder(
            "powershell.exe",
            "-NoProfile",
            "-NonInteractive",
            "-Command",
            "$ErrorActionPreference='Stop'\n" + script);
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

  static void requirePrivateFile(Path file) throws IOException {
    requirePrivate(file.getParent());
    if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.isSymbolicLink(file))
      throw new IOException("Host credential is not a plain file");
    var posix = Files.getFileAttributeView(file, PosixFileAttributeView.class);
    if (posix != null) {
      if (!posix
          .readAttributes()
          .permissions()
          .equals(PosixFilePermissions.fromString("rw-------")))
        throw new IOException("Host credential file is not private");
      return;
    }
    var parent = Files.getFileAttributeView(file.getParent(), AclFileAttributeView.class);
    var child = Files.getFileAttributeView(file, AclFileAttributeView.class);
    if (parent == null || child == null || !parent.getOwner().equals(child.getOwner()))
      throw new IOException("Host credential owner differs from private root");
    var readers = new HashSet<UserPrincipal>();
    for (var entry : parent.getAcl())
      if (entry.type() == AclEntryType.ALLOW
          && entry.permissions().contains(AclEntryPermission.READ_DATA))
        readers.add(entry.principal());
    for (var entry : child.getAcl())
      if (entry.type() == AclEntryType.ALLOW
          && entry.permissions().contains(AclEntryPermission.READ_DATA)
          && !readers.contains(entry.principal()))
        throw new IOException("Host credential file allows other readers");
  }
}
