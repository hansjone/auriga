package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.io.IOException;
import java.lang.foreign.*;
import java.lang.invoke.MethodHandle;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.util.*;
import java.util.concurrent.*;
import org.slf4j.LoggerFactory;
import org.springframework.context.SmartLifecycle;

/** Installation-only service JWT export, with atomic replacement and fixed subjects. */
public class ServiceTokenFilePublisher implements SmartLifecycle, AutoCloseable {
  private static final Set<PosixFilePermission> DIRECTORY =
      PosixFilePermissions.fromString("rwxr-x---");
  private static final Set<PosixFilePermission> FILE = PosixFilePermissions.fromString("rw-r-----");
  private final String service;
  private final InternalTokenIssuer issuer;
  private final Path secretFile;
  private final Path tokenFile;
  private final int readerGroup;
  private final int installationOwner;
  private final UserPrincipal processOwner;
  private ScheduledExecutorService scheduler;
  private boolean running;
  private long generation;
  private WindowsInstallation windows;

  ServiceTokenFilePublisher(
      String service,
      InternalTokenIssuer issuer,
      Path secretFile,
      Path tokenFile,
      int readerGroup,
      int installationOwner) {
    if (!Set.of("edge", "ai-gateway").contains(service)) throw invalid();
    this.service = service;
    this.issuer = Objects.requireNonNull(issuer);
    this.secretFile = absolute(secretFile);
    this.tokenFile = absolute(tokenFile);
    if (readerGroup < 0) throw invalid();
    this.readerGroup = readerGroup;
    if (installationOwner < -1) throw invalid();
    this.installationOwner = installationOwner;
    try {
      processOwner =
          tokenFile
              .getFileSystem()
              .getUserPrincipalLookupService()
              .lookupPrincipalByName(
                  System.getProperty("os.name").startsWith("Windows")
                      ? WindowsHandle.currentUser()
                      : System.getProperty("user.name"));
    } catch (IOException e) {
      throw invalid();
    }
    if (this.secretFile.equals(this.tokenFile)) throw invalid();
  }

  private static Path absolute(Path path) {
    if (!path.isAbsolute() || !path.equals(path.normalize()) || path.getParent() == null)
      throw invalid();
    return path;
  }

  private static IllegalStateException invalid() {
    return new IllegalStateException("Invalid private service token installation files");
  }

  @Override
  public synchronized void start() {
    if (running) return;
    try {
      if (tokenFile.getFileSystem().supportedFileAttributeViews().contains("acl")
          && System.getProperty("os.name").startsWith("Windows")) {
        if (installationOwner != -1) throw invalid(); // POSIX UIDs have no Windows meaning.
        windows = new WindowsInstallation();
      } else {
        checkAncestors(tokenFile.getParent().getParent());
        if (!Files.exists(tokenFile.getParent(), LinkOption.NOFOLLOW_LINKS)) {
          Files.createDirectory(
              tokenFile.getParent(), PosixFilePermissions.asFileAttribute(DIRECTORY));
          Files.setAttribute(
              tokenFile.getParent(), "unix:gid", readerGroup, LinkOption.NOFOLLOW_LINKS);
          Files.setPosixFilePermissions(tokenFile.getParent(), DIRECTORY);
        }
        checkDirectory(tokenFile.getParent(), false);
        checkOutputGroup();
        checkDirectory(secretFile.getParent(), true);
      }
    } catch (IOException | UnsupportedOperationException e) {
      if (windows != null) windows.close();
      windows = null;
      throw invalid();
    }
    running = true;
    generation++;
    scheduler =
        Executors.newSingleThreadScheduledExecutor(
            Thread.ofPlatform().name("iam-" + service + "-token").daemon(true).factory());
    final long scheduledGeneration = generation;
    scheduler.scheduleWithFixedDelay(() -> refresh(scheduledGeneration), 0, 60, TimeUnit.SECONDS);
  }

  /**
   * Each attempt reauthenticates with the existing issuer; a failure never rewrites the old JWT.
   */
  boolean refresh() {
    final long activeGeneration;
    synchronized (this) {
      activeGeneration = generation;
    }
    return refresh(activeGeneration);
  }

  private boolean refresh(long activeGeneration) {
    synchronized (this) {
      if (!running || generation != activeGeneration) return false;
      if (windows != null) return refreshWindows();
    }
    byte[] raw = null;
    Path temporary = null;
    try {
      checkDirectory(secretFile.getParent(), true);
      checkDirectory(tokenFile.getParent(), false);
      checkOutputGroup();
      try (var secrets = secure(secretFile.getParent());
          var output = secure(tokenFile.getParent())) {
        checkFile(secrets, secretFile, true, 128);
        try (var channel =
            secrets.newByteChannel(
                secretFile.getFileName(),
                Set.of(StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS))) {
          var bytes = ByteBuffer.allocate(129);
          while (bytes.hasRemaining() && channel.read(bytes) != -1) {}
          raw = Arrays.copyOf(bytes.array(), bytes.position());
          Arrays.fill(bytes.array(), (byte) 0);
        }
        String secret = new String(raw, java.nio.charset.StandardCharsets.US_ASCII);
        if (!secret.matches("[A-Za-z0-9_-]{43}")) throw invalid();
        String jwt = issuer.issue(service, secret);
        if (jwt == null
            || jwt.length() > 16 * 1024
            || !jwt.matches("[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+")) throw invalid();
        synchronized (this) {
          if (!running || generation != activeGeneration) return false;
          checkFile(output, tokenFile, false, 16 * 1024);
          temporary = Path.of("." + service + "-" + UUID.randomUUID() + ".tmp");
          try (var channel =
              output.newByteChannel(
                  temporary,
                  Set.of(
                      StandardOpenOption.CREATE_NEW,
                      StandardOpenOption.WRITE,
                      LinkOption.NOFOLLOW_LINKS),
                  PosixFilePermissions.asFileAttribute(FILE))) {
            byte[] encoded = jwt.getBytes(java.nio.charset.StandardCharsets.US_ASCII);
            try {
              var bytes = ByteBuffer.wrap(encoded);
              while (bytes.hasRemaining()) channel.write(bytes);
              if (channel instanceof FileChannel file) file.force(true);
            } finally {
              Arrays.fill(encoded, (byte) 0);
            }
          }
          Files.setAttribute(
              tokenFile.getParent().resolve(temporary),
              "unix:gid",
              readerGroup,
              LinkOption.NOFOLLOW_LINKS);
          Files.setPosixFilePermissions(tokenFile.getParent().resolve(temporary), FILE);
          checkFile(output, tokenFile.getParent().resolve(temporary), false, 16 * 1024);
          checkDirectory(tokenFile.getParent(), false);
          checkOutputGroup();
          checkFile(output, tokenFile, false, 16 * 1024);
          // SecureDirectoryStream.move is renameat: atomic replacement within this pinned
          // directory.
          output.move(temporary, output, tokenFile.getFileName());
          temporary = null;
        }
      }
      return true;
    } catch (RuntimeException | IOException e) {
      // Never log the exception: issuers/filesystem providers may include credentials or contents.
      LoggerFactory.getLogger(ServiceTokenFilePublisher.class)
          .warn("Service token refresh failed; previous token expiry is unchanged");
      return false;
    } finally {
      if (raw != null) Arrays.fill(raw, (byte) 0);
      if (temporary != null) {
        try (var output = secure(tokenFile.getParent())) {
          output.deleteFile(temporary);
        } catch (IOException | RuntimeException ignored) {
          // The private directory remains inaccessible to untrusted users.
        }
      }
    }
  }

  // Windows readers use the same account as the publisher. The numeric POSIX reader-group is
  // deliberately not translated into a broader Windows group; private ACLs are owner-only.
  private boolean refreshWindows() {
    Path temporary = null;
    byte[] raw = null;
    try {
      windows.checkPrivate(secretFile.getParent(), true);
      windows.checkPrivate(tokenFile.getParent(), true);
      try (var pinned = WindowsHandle.open(secretFile, false)) {
        windows.checkPrivate(secretFile, false);
        if (Files.size(secretFile) < 1 || Files.size(secretFile) > 128) throw invalid();
        raw = Files.readAllBytes(secretFile);
      }
      String secret = new String(raw, java.nio.charset.StandardCharsets.US_ASCII);
      if (!secret.matches("[A-Za-z0-9_-]{43}")) throw invalid();
      String jwt = issuer.issue(service, secret);
      if (jwt == null
          || jwt.length() > 16 * 1024
          || !jwt.matches("[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+\\.[A-Za-z0-9_-]+")) throw invalid();
      windows.checkToken();
      temporary = tokenFile.resolveSibling("." + service + "-" + UUID.randomUUID() + ".tmp");
      Files.createFile(temporary, windows.privateAcl());
      // Elevated Windows tokens can default new objects to Administrators ownership.
      // Normalize only this newly created file; existing installation owners stay strict.
      if (!Files.getOwner(temporary, LinkOption.NOFOLLOW_LINKS).equals(processOwner))
        Files.setOwner(temporary, processOwner);
      windows.checkPrivate(temporary, false);
      byte[] encoded = jwt.getBytes(java.nio.charset.StandardCharsets.US_ASCII);
      try (var channel =
          FileChannel.open(temporary, StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS)) {
        var bytes = ByteBuffer.wrap(encoded);
        while (bytes.hasRemaining()) channel.write(bytes);
        channel.force(true);
      } finally {
        Arrays.fill(encoded, (byte) 0);
      }
      windows.checkPrivate(tokenFile.getParent(), true);
      windows.checkToken();
      Files.move(
          temporary,
          tokenFile,
          StandardCopyOption.ATOMIC_MOVE,
          StandardCopyOption.REPLACE_EXISTING);
      temporary = null;
      return true;
    } catch (IOException | RuntimeException e) {
      LoggerFactory.getLogger(ServiceTokenFilePublisher.class)
          .warn("Service token refresh failed; previous token expiry is unchanged");
      return false;
    } finally {
      if (raw != null) Arrays.fill(raw, (byte) 0);
      if (temporary != null) {
        try {
          Files.deleteIfExists(temporary);
        } catch (IOException ignored) {
        }
      }
    }
  }

  private final class WindowsInstallation implements AutoCloseable {
    private final List<WindowsHandle> directories = new ArrayList<>();

    WindowsInstallation() throws IOException {
      try {
        Set<Path> pinned = new HashSet<>();
        // Hold every ancestor without FILE_SHARE_DELETE for the entire lifecycle. This prevents
        // path substitution, including through a writable ancestor, between refresh attempts.
        for (Path parent : List.of(secretFile.getParent(), tokenFile.getParent())) {
          Path current = parent.getRoot();
          if (pinned.add(current)) directories.add(WindowsHandle.open(current, true));
          for (Path component : parent) {
            current = current.resolve(component);
            if (!Files.exists(current, LinkOption.NOFOLLOW_LINKS)
                && current.equals(tokenFile.getParent())) {
              Files.createDirectory(current, privateAcl());
              if (!Files.getOwner(current, LinkOption.NOFOLLOW_LINKS).equals(processOwner))
                Files.setOwner(current, processOwner);
            }
            if (pinned.add(current)) directories.add(WindowsHandle.open(current, true));
          }
        }
        checkPrivate(secretFile.getParent(), true);
        checkPrivate(tokenFile.getParent(), true);
        checkPrivate(secretFile, false);
        checkToken();
      } catch (IOException | RuntimeException e) {
        close();
        throw e;
      }
    }

    FileAttribute<List<AclEntry>> privateAcl() {
      List<AclEntry> acl =
          List.of(
              AclEntry.newBuilder()
                  .setType(AclEntryType.ALLOW)
                  .setPrincipal(processOwner)
                  .setPermissions(EnumSet.allOf(AclEntryPermission.class))
                  .build());
      return new FileAttribute<>() {
        public String name() {
          return "acl:acl";
        }

        public List<AclEntry> value() {
          return acl;
        }
      };
    }

    void checkPrivate(Path path, boolean directory) throws IOException {
      var basic = Files.readAttributes(path, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
      if (basic.isSymbolicLink()
          || basic.isOther()
          || (directory ? !basic.isDirectory() : !basic.isRegularFile())) throw invalid();
      var view =
          Files.getFileAttributeView(path, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
      if (view == null || !processOwner.equals(view.getOwner())) throw invalid();
      if (directory) {
        try (var pinned = WindowsHandle.open(path, true)) {
          if (!pinned.protectedDacl()) throw invalid();
        }
      }
      boolean ownerRead = false;
      for (AclEntry entry : view.getAcl()) {
        if (entry.type() != AclEntryType.ALLOW) continue;
        // Even read access to installation secrets/JWTs is denied to other principals.
        if (!entry.principal().equals(processOwner) && !entry.permissions().isEmpty())
          throw invalid();
        if (entry.principal().equals(processOwner)
            && entry.permissions().contains(AclEntryPermission.READ_DATA)) ownerRead = true;
      }
      if (!ownerRead) throw invalid();
    }

    void checkToken() throws IOException {
      if (!Files.exists(tokenFile, LinkOption.NOFOLLOW_LINKS)) return;
      try (var pinned = WindowsHandle.open(tokenFile, false)) {
        checkPrivate(tokenFile, false);
        if (Files.size(tokenFile) < 1 || Files.size(tokenFile) > 16 * 1024) throw invalid();
      }
    }

    @Override
    public void close() {
      for (int i = directories.size() - 1; i >= 0; i--) directories.get(i).close();
      directories.clear();
    }
  }

  /** Minimal Win32 handles: no shell invocation and no credentials cross a process boundary. */
  private static final class WindowsHandle implements AutoCloseable {
    private static final Linker LINKER = Linker.nativeLinker();
    private static final SymbolLookup KERNEL =
        SymbolLookup.libraryLookup("kernel32", Arena.global());
    private static final MethodHandle CREATE =
        function(
            "CreateFileW",
            ValueLayout.ADDRESS,
            ValueLayout.ADDRESS,
            ValueLayout.JAVA_INT,
            ValueLayout.JAVA_INT,
            ValueLayout.ADDRESS,
            ValueLayout.JAVA_INT,
            ValueLayout.JAVA_INT,
            ValueLayout.ADDRESS);
    private static final MethodHandle INFO =
        function(
            "GetFileInformationByHandle",
            ValueLayout.JAVA_INT,
            ValueLayout.ADDRESS,
            ValueLayout.ADDRESS);
    private static final MethodHandle CLOSE =
        function("CloseHandle", ValueLayout.JAVA_INT, ValueLayout.ADDRESS);
    private static final SymbolLookup ADVAPI =
        SymbolLookup.libraryLookup("advapi32", Arena.global());
    private static final MethodHandle SECURITY =
        LINKER.downcallHandle(
            ADVAPI.find("GetKernelObjectSecurity").orElseThrow(),
            FunctionDescriptor.of(
                ValueLayout.JAVA_INT,
                ValueLayout.ADDRESS,
                ValueLayout.JAVA_INT,
                ValueLayout.ADDRESS,
                ValueLayout.JAVA_INT,
                ValueLayout.ADDRESS));
    private static final SymbolLookup SECUR32 =
        SymbolLookup.libraryLookup("secur32", Arena.global());
    private static final MethodHandle USER =
        LINKER.downcallHandle(
            SECUR32.find("GetUserNameExW").orElseThrow(),
            FunctionDescriptor.of(
                ValueLayout.JAVA_BYTE,
                ValueLayout.JAVA_INT,
                ValueLayout.ADDRESS,
                ValueLayout.ADDRESS));
    private final MemorySegment handle;

    static String currentUser() throws IOException {
      try (var arena = Arena.ofConfined()) {
        var name = arena.allocate(1024, 2);
        var length = arena.allocate(ValueLayout.JAVA_INT);
        length.set(ValueLayout.JAVA_INT, 0, 512);
        if ((byte) USER.invokeExact(2, name, length) == 0) throw invalid();
        return name.getString(0, java.nio.charset.StandardCharsets.UTF_16LE);
      } catch (Throwable e) {
        throw new IOException("Cannot determine service process identity");
      }
    }

    private WindowsHandle(MemorySegment handle) {
      this.handle = handle;
    }

    private static MethodHandle function(String name, MemoryLayout result, MemoryLayout... args) {
      return LINKER.downcallHandle(
          KERNEL.find(name).orElseThrow(), FunctionDescriptor.of(result, args));
    }

    static WindowsHandle open(Path path, boolean directory) throws IOException {
      WindowsHandle opened = null;
      try (var arena = Arena.ofConfined()) {
        if (path.toString().startsWith("\\\\")) throw invalid(); // Only local volumes.
        var name = arena.allocateFrom(path.toString(), java.nio.charset.StandardCharsets.UTF_16LE);
        // UTF-16 allocation needs a second zero byte beyond allocateFrom's single terminator.
        var wide = arena.allocate((path.toString().length() + 1L) * 2, 2);
        wide.copyFrom(name.asSlice(0, path.toString().length() * 2L));
        MemorySegment handle =
            (MemorySegment)
                CREATE.invokeExact(
                    wide,
                    0x80000000,
                    directory ? 3 : 1,
                    MemorySegment.NULL,
                    3,
                    0x02200000,
                    MemorySegment.NULL);
        if (handle.address() == -1L) throw invalid();
        opened = new WindowsHandle(handle);
        var info = arena.allocate(52, 4);
        if ((int) INFO.invokeExact(handle, info) == 0) throw invalid();
        int attributes = info.get(ValueLayout.JAVA_INT, 0);
        if ((attributes & 0x400) != 0
            || ((attributes & 0x10) != 0) != directory
            || (!directory && info.get(ValueLayout.JAVA_INT, 40) != 1)) throw invalid();
        return opened;
      } catch (Throwable e) {
        if (opened != null) opened.close();
        throw new IOException("Invalid private service token installation files");
      }
    }

    boolean protectedDacl() throws IOException {
      try (var arena = Arena.ofConfined()) {
        var descriptor = arena.allocate(65536, 4);
        var length = arena.allocate(ValueLayout.JAVA_INT);
        if ((int) SECURITY.invokeExact(handle, 4, descriptor, 65536, length) == 0) throw invalid();
        return (descriptor.get(ValueLayout.JAVA_SHORT, 2) & 0x1000) != 0;
      } catch (Throwable e) {
        throw new IOException("Cannot verify private directory DACL");
      }
    }

    @Override
    public void close() {
      try {
        int ignored = (int) CLOSE.invokeExact(handle);
      } catch (Throwable e) {
        throw invalid();
      }
    }
  }

  private static SecureDirectoryStream<Path> secure(Path directory) throws IOException {
    var stream = Files.newDirectoryStream(directory);
    if (stream instanceof SecureDirectoryStream<Path> secure) return secure;
    stream.close();
    throw invalid();
  }

  private void checkAncestors(Path directory) throws IOException {
    if (directory == null) throw invalid();
    for (Path p = directory; p != null; p = p.getParent()) {
      var a = Files.readAttributes(p, PosixFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
      if (!a.isDirectory() || a.isSymbolicLink()) throw invalid();
      int uid = (int) Files.getAttribute(p, "unix:uid", LinkOption.NOFOLLOW_LINKS);
      if (uid != 0 && !a.owner().equals(processOwner) && uid != installationOwner) throw invalid();
      // A shared sticky ancestor (e.g. /tmp) cannot rename another owner's private child.
      boolean writable =
          a.permissions().contains(PosixFilePermission.GROUP_WRITE)
              || a.permissions().contains(PosixFilePermission.OTHERS_WRITE);
      if (writable
          && (((int) Files.getAttribute(p, "unix:mode", LinkOption.NOFOLLOW_LINKS)) & 01000) == 0)
        throw invalid();
    }
  }

  private void checkDirectory(Path directory, boolean secret) throws IOException {
    checkAncestors(directory.getParent());
    var a = Files.readAttributes(directory, PosixFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
    Set<PosixFilePermission> allowed =
        secret ? PosixFilePermissions.fromString("rwx------") : DIRECTORY;
    if (!a.isDirectory()
        || !allowed.containsAll(a.permissions())
        || !a.permissions().containsAll(PosixFilePermissions.fromString("rwx------")))
      throw invalid();
    if (secret) {
      if (installationOwner == -1
          ? !a.owner().equals(processOwner)
          : !Files.getAttribute(directory, "unix:uid", LinkOption.NOFOLLOW_LINKS)
              .equals(installationOwner)) throw invalid();
    }
  }

  private void checkOutputGroup() throws IOException {
    var a =
        Files.readAttributes(
            tokenFile.getParent(), PosixFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
    if (!a.owner().equals(processOwner)
        || !a.permissions().equals(DIRECTORY)
        || !Files.getAttribute(tokenFile.getParent(), "unix:gid", LinkOption.NOFOLLOW_LINKS)
            .equals(readerGroup)) throw invalid();
  }

  private static void checkFile(
      SecureDirectoryStream<Path> directory, Path path, boolean required, int limit)
      throws IOException {
    Path file = path.getFileName();
    PosixFileAttributes a;
    try {
      a =
          directory
              .getFileAttributeView(file, PosixFileAttributeView.class, LinkOption.NOFOLLOW_LINKS)
              .readAttributes();
    } catch (NoSuchFileException e) {
      if (required) throw e;
      return;
    }
    var parent = directory.getFileAttributeView(PosixFileAttributeView.class).readAttributes();
    var allowed = required ? PosixFilePermissions.fromString("rw-------") : FILE;
    if (!a.isRegularFile()
        || a.size() < 1
        || a.size() > limit
        || !allowed.containsAll(a.permissions())
        || !a.owner().equals(parent.owner())
        || !a.group().equals(parent.group())) throw invalid();
    var linked = Files.readAttributes(path, "unix:nlink", LinkOption.NOFOLLOW_LINKS);
    if (!Integer.valueOf(1).equals(linked.get("nlink"))
        || !a.fileKey()
            .equals(
                Files.readAttributes(path, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS)
                    .fileKey())) throw invalid();
  }

  @Override
  public synchronized void stop() {
    running = false;
    generation++;
    if (scheduler != null) {
      scheduler.shutdownNow();
      scheduler = null;
    }
    if (windows != null) {
      try {
        windows.checkPrivate(tokenFile.getParent(), true);
        windows.checkToken();
        Files.deleteIfExists(tokenFile);
      } catch (IOException | RuntimeException ignored) {
        // An unchanged old signed token expires normally if cleanup is impossible.
      } finally {
        windows.close();
        windows = null;
      }
      return;
    }
    try {
      checkDirectory(tokenFile.getParent(), false);
      checkOutputGroup();
      try (var output = secure(tokenFile.getParent())) {
        checkFile(output, tokenFile, false, 16 * 1024);
        output.deleteFile(tokenFile.getFileName());
      }
    } catch (IOException | RuntimeException ignored) {
      // A damaged/missing path fails closed at the reader; an old signed JWT still expires
      // normally.
    }
  }

  @Override
  public synchronized boolean isRunning() {
    return running;
  }

  @Override
  public void close() {
    stop();
  }
}
