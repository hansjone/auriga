package auriga.iam;

import java.io.*;
import java.nio.channels.FileChannel;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.*;
import java.util.zip.*;

/** User-only temporary output. Fixed entries, CREATE_NEW and atomic publication only. */
final class UacDiagnosticTemp implements AutoCloseable {
  private static final Set<String> NAMES =
      Set.of(
          "manifest.json",
          "uac-steps.jsonl",
          "audit.jsonl",
          "ops-alerts.json",
          "sanitized-config.json",
          "uds-detail.jsonl");
  private final List<ServiceTokenFilePublisher.WindowsHandle> parents = new ArrayList<>();
  private final boolean windows = System.getProperty("os.name").startsWith("Windows");
  private final UserPrincipal owner;
  final Path directory;
  private ServiceTokenFilePublisher.WindowsHandle directoryHandle;

  UacDiagnosticTemp(Path root) throws IOException {
    if (!root.isAbsolute() || !root.equals(root.normalize())) throw invalid();
    owner =
        root.getFileSystem()
            .getUserPrincipalLookupService()
            .lookupPrincipalByName(
                windows
                    ? ServiceTokenFilePublisher.WindowsHandle.currentUser()
                    : System.getProperty("user.name"));
    Path made = null;
    try {
      Path current = root.getRoot();
      checkParent(current);
      for (Path component : root) {
        current = current.resolve(component);
        checkParent(current);
      }
      made =
          windows
              ? Files.createTempDirectory(root, "auriga-uac-", acl())
              : Files.createTempDirectory(
                  root,
                  "auriga-uac-",
                  PosixFilePermissions.asFileAttribute(
                      PosixFilePermissions.fromString("rwx------")));
      if (!made.getParent().equals(root)) throw invalid();
      if (!owner.equals(Files.getOwner(made, LinkOption.NOFOLLOW_LINKS)))
        Files.setOwner(made, owner);
      if (windows) {
        Files.getFileAttributeView(made, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS)
            .setAcl(acl().value());
        directoryHandle = ServiceTokenFilePublisher.WindowsHandle.open(made, true, true);
        directoryHandle.protectDacl();
      }
      privateFile(made, true);
      directory = made;
    } catch (IOException | RuntimeException failure) {
      if (directoryHandle != null) directoryHandle.close();
      if (made != null) Files.deleteIfExists(made);
      closeParents();
      throw invalid();
    }
  }

  private void checkParent(Path path) throws IOException {
    var attributes =
        Files.readAttributes(path, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
    if (attributes.isSymbolicLink() || attributes.isOther() || !attributes.isDirectory())
      throw invalid();
    if (windows) parents.add(ServiceTokenFilePublisher.WindowsHandle.open(path, true));
  }

  private FileAttribute<List<AclEntry>> acl() {
    List<AclEntry> entries =
        List.of(
            AclEntry.newBuilder()
                .setType(AclEntryType.ALLOW)
                .setPrincipal(owner)
                .setPermissions(EnumSet.allOf(AclEntryPermission.class))
                .build());
    return new FileAttribute<>() {
      public String name() {
        return "acl:acl";
      }

      public List<AclEntry> value() {
        return entries;
      }
    };
  }

  private void privateFile(Path path, boolean directory) throws IOException {
    var attributes =
        Files.readAttributes(path, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
    if (attributes.isSymbolicLink()
        || attributes.isOther()
        || (directory ? !attributes.isDirectory() : !attributes.isRegularFile())
        || !owner.equals(Files.getOwner(path, LinkOption.NOFOLLOW_LINKS))) throw invalid();
    if (windows) {
      for (AclEntry entry :
          Files.getFileAttributeView(path, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS)
              .getAcl())
        if (entry.type() == AclEntryType.ALLOW
            && !entry.principal().equals(owner)
            && !entry.permissions().isEmpty()) throw invalid();
    } else {
      var permissions = Files.getPosixFilePermissions(path, LinkOption.NOFOLLOW_LINKS);
      for (PosixFilePermission permission : permissions)
        if (permission.name().startsWith("GROUP_") || permission.name().startsWith("OTHERS_"))
          throw invalid();
    }
  }

  byte[] publish(Map<String, byte[]> entries, int limit) throws IOException {
    if (!NAMES.containsAll(entries.keySet()) || !entries.containsKey("manifest.json"))
      throw invalid();
    long sum = 0;
    for (byte[] value : entries.values()) {
      sum += value.length;
      if (sum > limit) throw invalid();
    }
    privateFile(directory, true);
    Path temporary = directory.resolve("bundle.part"), published = directory.resolve("bundle.zip");
    if (Files.exists(temporary, LinkOption.NOFOLLOW_LINKS)
        || Files.exists(published, LinkOption.NOFOLLOW_LINKS)) throw invalid();
    if (windows) Files.createFile(temporary, acl());
    else
      Files.createFile(
          temporary,
          PosixFilePermissions.asFileAttribute(PosixFilePermissions.fromString("rw-------")));
    if (!owner.equals(Files.getOwner(temporary, LinkOption.NOFOLLOW_LINKS)))
      Files.setOwner(temporary, owner);
    privateFile(temporary, false);
    try (var output =
        new ZipOutputStream(
            Files.newOutputStream(
                temporary, StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS))) {
      for (var entry : entries.entrySet()) {
        output.putNextEntry(new ZipEntry(entry.getKey()));
        output.write(entry.getValue());
        output.closeEntry();
      }
    }
    try (var channel =
        FileChannel.open(temporary, StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS)) {
      channel.force(true);
    }
    if (Files.size(temporary) > limit) throw invalid();
    Files.move(temporary, published, StandardCopyOption.ATOMIC_MOVE);
    privateFile(published, false);
    if (windows)
      try (var handle = ServiceTokenFilePublisher.WindowsHandle.open(published, false, true)) {
        handle.protectDacl();
        return Files.readAllBytes(published);
      }
    return Files.readAllBytes(published);
  }

  @Override
  public void close() throws IOException {
    try {
      Files.deleteIfExists(directory.resolve("bundle.part"));
      Files.deleteIfExists(directory.resolve("bundle.zip"));
      if (directoryHandle != null) {
        directoryHandle.close();
        directoryHandle = null;
      }
      Files.delete(directory);
    } finally {
      closeParents();
    }
  }

  private void closeParents() {
    for (int i = parents.size() - 1; i >= 0; i--) parents.get(i).close();
    parents.clear();
  }

  private static IOException invalid() {
    return new IOException("Invalid private diagnostic output");
  }
}
