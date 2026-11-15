package auriga.iam;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.*;

/** Reads one immutable installation snapshot while holding its path against substitution. */
final class InstallationPinFile {
  private static final int LIMIT = 64 * 1024 * 1024;

  static byte[] read(Path file) throws IOException {
    if (!file.isAbsolute()
        || !file.equals(file.normalize())
        || file.getParent() == null
        || file.getParent().equals(file.getRoot())) throw invalid();
    if (file.getFileSystem().supportedFileAttributeViews().contains("posix")) return posix(file);
    if (file.getFileSystem().supportedFileAttributeViews().contains("acl")) return windows(file);
    throw invalid();
  }

  private static UserPrincipal owner(Path file, boolean windows) throws IOException {
    String user =
        windows
            ? ServiceTokenFilePublisher.WindowsHandle.currentUser()
            : ProcessHandle.current().info().user().orElseThrow(InstallationPinFile::invalid);
    return file.getFileSystem().getUserPrincipalLookupService().lookupPrincipalByName(user);
  }

  private static SecureDirectoryStream<Path> secure(Path root) throws IOException {
    var stream = Files.newDirectoryStream(root);
    if (stream instanceof SecureDirectoryStream<Path> secure) return secure;
    stream.close();
    throw invalid();
  }

  private static byte[] posix(Path file) throws IOException {
    var process = owner(file, false);
    var held = new ArrayList<SecureDirectoryStream<Path>>();
    try {
      var directory = secure(file.getRoot());
      held.add(directory);
      Path current = file.getRoot();
      // Resolve every component relative to a pinned directory descriptor. NOFOLLOW applies
      // to each component, so a concurrent rename cannot redirect the final read.
      for (Path part : file.getParent()) {
        var attributes =
            directory
                .getFileAttributeView(part, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS)
                .readAttributes();
        if (!attributes.isDirectory() || attributes.isSymbolicLink()) throw invalid();
        current = current.resolve(part);
        directory = directory.newDirectoryStream(part, LinkOption.NOFOLLOW_LINKS);
        held.add(directory);
        if (current.equals(file.getParent())) {
          var pinned =
              directory.getFileAttributeView(PosixFileAttributeView.class).readAttributes();
          if (!pinned.owner().equals(process)
              || !pinned.permissions().equals(PosixFilePermissions.fromString("rwx------")))
            throw invalid();
        }
      }
      var view =
          directory.getFileAttributeView(
              file.getFileName(), PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
      var before = view.readAttributes();
      if (!before.isRegularFile()
          || !before.owner().equals(process)
          || before.size() > LIMIT
          || !before.permissions().equals(PosixFilePermissions.fromString("rw-------")))
        throw invalid();
      try (var channel =
          directory.newByteChannel(
              file.getFileName(), Set.of(StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS))) {
        var result = new ByteArrayOutputStream();
        var buffer = ByteBuffer.allocate(8192);
        while (channel.read(buffer) != -1) {
          if ((long) result.size() + buffer.position() > LIMIT) throw invalid();
          result.write(buffer.array(), 0, buffer.position());
          buffer.clear();
        }
        var after = view.readAttributes();
        if (!Objects.equals(before.fileKey(), after.fileKey())
            || before.size() != after.size()
            || !before.lastModifiedTime().equals(after.lastModifiedTime())) throw invalid();
        return result.toByteArray();
      }
    } finally {
      for (int i = held.size() - 1; i >= 0; i--) held.get(i).close();
    }
  }

  private static byte[] windows(Path file) throws IOException {
    var held = new ArrayList<ServiceTokenFilePublisher.WindowsHandle>();
    var process = owner(file, true);
    try {
      Path current = file.getRoot();
      held.add(ServiceTokenFilePublisher.WindowsHandle.open(current, true));
      for (Path part : file.getParent()) {
        current = current.resolve(part);
        held.add(ServiceTokenFilePublisher.WindowsHandle.open(current, true));
      }
      held.add(ServiceTokenFilePublisher.WindowsHandle.open(file, false));
      for (Path path : List.of(file.getParent(), file)) {
        var view =
            Files.getFileAttributeView(path, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
        if (view == null || !view.getOwner().equals(process)) throw invalid();
        boolean readable = false;
        for (var ace : view.getAcl()) {
          if (ace.type() != AclEntryType.ALLOW || ace.permissions().isEmpty()) continue;
          String name = ace.principal().getName();
          if (!ace.principal().equals(process)
              && !name.equals("NT AUTHORITY\\SYSTEM")
              && !name.equals("BUILTIN\\Administrators")) throw invalid();
          readable |=
              ace.principal().equals(process)
                  && ace.permissions().contains(AclEntryPermission.READ_DATA);
        }
        if (!readable) throw invalid();
      }
      // The file handle denies writes/deletes and all ancestors deny delete sharing.
      if (Files.size(file) > LIMIT) throw invalid();
      return Files.readAllBytes(file);
    } finally {
      for (int i = held.size() - 1; i >= 0; i--) held.get(i).close();
    }
  }

  private static IOException invalid() {
    return new IOException("unprotected installation pin file");
  }

  private InstallationPinFile() {}
}
