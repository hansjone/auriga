package auriga.testsupport;

import java.io.IOException;
import java.io.InputStream;
import java.io.UncheckedIOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.channels.FileChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.time.Instant;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import java.util.stream.Stream;
import java.util.zip.GZIPInputStream;
import java.util.zip.ZipInputStream;

/**
 * A real {@code nats-server} (JetStream enabled) as a child process for tests.
 *
 * <p>There is no embeddable Java NATS server, so the official release binary is downloaded once
 * into a shared cache ({@code -Dauriga.test.cache}, env {@code AURIGA_TEST_CACHE}, default {@code
 * ${user.home}/.cache/auriga}), verified against the pinned SHA-256 and reused by every build.
 * {@code AURIGA_NATS_SERVER} may point at a pre-installed binary instead (offline CI). The server
 * listens on 127.0.0.1 with a random port, stores JetStream data in a temp directory, and is killed
 * when closed.
 */
public final class NatsServer implements AutoCloseable {
  public static final String VERSION = "2.15.1";

  /** SHA-256 of the release archives (from the release's SHA256SUMS). */
  private static final Map<String, String> CHECKSUMS =
      Map.of(
          "windows-amd64.zip", "4f7edfa376750b2720463bd22766ed469a5f13f378a643097866fcd953cbf7e3",
          "windows-arm64.zip", "888a62b665b3123edde84c2e39751e4d9ced1e0dc7a7a234a40c53f2bba40b6f",
          "linux-amd64.tar.gz", "bf5edc7b1ce98e885c368cb43a886eb148dc5d0551d99f3cf6141fb5e1954c70",
          "linux-arm64.tar.gz", "e2fbab4cad7d3c76e3052eb959570393c9d73ba756b70bf8e9eea61e9ae6d49c",
          "darwin-amd64.tar.gz", "3cba5976a4fbd022669d3401cab4147faeb2b4c079712c882edb9ae4e52feb06",
          "darwin-arm64.tar.gz",
              "ed0fe39222ec2e317c4d0e1a9cdc16c63ec523d94f9355d24a902a4db2655ffd");

  /** SHA-256 of the extracted executable; checked on every use, not only after download. */
  private static final Map<String, String> BINARY_CHECKSUMS =
      Map.of(
          "windows-amd64.zip", "308b0ac72d7079a19f7221bf2326a3c8abfe861be0034fd73d9da444ca3fe055",
          "windows-arm64.zip", "333672c7104470d9ceab827d37b6e5e355b160bcefd6ce434e9b6683b5c73c4b",
          "linux-amd64.tar.gz", "baab2eecb5a8ebdabf6edd3e22756f89805e3184468653ba67185adf5a3b57eb",
          "linux-arm64.tar.gz", "eda00ae9bf54f28bb0ca640d80fca0e3033a722f107f0ec311791decb0ab436d",
          "darwin-amd64.tar.gz", "bdaac5b753ba5e1b6cb18c99b2648b3aa6c8f53c90a3619bc961dd6b0a3c6348",
          "darwin-arm64.tar.gz",
              "b52d3bba3977dacfab306279ea2cc1e9903219598b64c04aa32b0664bdb68195");

  private static final Pattern URL = Pattern.compile("\"(nats://[^\"]+)\"");

  private final Process process;
  private final Path workDir;
  private final String url;

  private NatsServer(Process process, Path workDir, String url) {
    this.process = process;
    this.workDir = workDir;
    this.url = url;
  }

  /** Client URL, e.g. {@code nats://127.0.0.1:41234}. */
  public String url() {
    return url;
  }

  public static NatsServer start() {
    Path binary = binary();
    try {
      Path work = Files.createTempDirectory("auriga-nats-");
      Path store = Files.createDirectories(work.resolve("js"));
      Process process =
          new ProcessBuilder(
                  binary.toString(),
                  "-a",
                  "127.0.0.1",
                  "-p",
                  "-1",
                  "-js",
                  "-sd",
                  store.toString(),
                  "--ports_file_dir",
                  work.toString())
              .redirectErrorStream(true)
              .redirectOutput(work.resolve("nats-server.log").toFile())
              .start();
      Runtime.getRuntime().addShutdownHook(new Thread(process::destroyForcibly));
      // Written as <executable name>_<pid>.ports once the listener is up.
      String portsSuffix = "_" + process.pid() + ".ports";
      Instant deadline = Instant.now().plusSeconds(30);
      while (Instant.now().isBefore(deadline)) {
        if (!process.isAlive()) {
          throw new IllegalStateException(
              "nats-server exited: " + Files.readString(work.resolve("nats-server.log")));
        }
        try (Stream<Path> files = Files.list(work)) {
          var portsFile =
              files.filter(f -> f.getFileName().toString().endsWith(portsSuffix)).findFirst();
          if (portsFile.isPresent()) {
            Matcher m = URL.matcher(Files.readString(portsFile.get()));
            if (m.find()) return new NatsServer(process, work, m.group(1));
          }
        }
        Thread.sleep(50);
      }
      process.destroyForcibly();
      throw new IllegalStateException("nats-server did not report its port in time");
    } catch (IOException e) {
      throw new UncheckedIOException(e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException(e);
    }
  }

  @Override
  public void close() {
    process.destroy();
    try {
      if (!process.waitFor(10, java.util.concurrent.TimeUnit.SECONDS)) {
        process.destroyForcibly().waitFor(10, java.util.concurrent.TimeUnit.SECONDS);
      }
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
    }
    deleteQuietly(workDir);
  }

  public boolean isAlive() {
    return process.isAlive();
  }

  // ---------------------------------------------------------------- binary management

  static String platform() {
    String os = System.getProperty("os.name").toLowerCase(Locale.ROOT);
    String arch = System.getProperty("os.arch").toLowerCase(Locale.ROOT);
    String a = arch.contains("aarch64") || arch.contains("arm64") ? "arm64" : "amd64";
    if (os.contains("win")) return "windows-" + a + ".zip";
    if (os.contains("mac") || os.contains("darwin")) return "darwin-" + a + ".tar.gz";
    return "linux-" + a + ".tar.gz";
  }

  static Path cacheRoot() {
    String configured = System.getProperty("auriga.test.cache", System.getenv("AURIGA_TEST_CACHE"));
    if (configured != null && !configured.isBlank()) return Path.of(configured);
    return Path.of(System.getProperty("user.home"), ".cache", "auriga");
  }

  static synchronized Path binary() {
    String preinstalled = System.getenv("AURIGA_NATS_SERVER");
    if (preinstalled != null && !preinstalled.isBlank()) return Path.of(preinstalled);
    String platform = platform();
    String checksum = CHECKSUMS.get(platform);
    if (checksum == null) throw new IllegalStateException("Unsupported platform " + platform);
    boolean windows = platform.startsWith("windows");
    Path dir = cacheRoot().resolve("nats-server").resolve(VERSION).resolve(platform);
    Path exe = dir.resolve(windows ? "nats-server.exe" : "nats-server");
    if (Files.isExecutable(exe) && binaryMatches(exe, platform)) return exe;
    try {
      Files.createDirectories(dir);
      try (FileChannel lock =
              FileChannel.open(
                  dir.resolve(".lock"), StandardOpenOption.CREATE, StandardOpenOption.WRITE);
          var ignored = lock.lock()) {
        if (Files.isExecutable(exe) && binaryMatches(exe, platform)) return exe;
        String name = "nats-server-v" + VERSION + "-" + platform;
        byte[] archive =
            download(
                URI.create(
                    "https://github.com/nats-io/nats-server/releases/download/v"
                        + VERSION
                        + "/"
                        + name));
        String actual =
            HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(archive));
        if (!actual.equals(checksum)) {
          throw new IllegalStateException("Checksum mismatch for " + name + ": " + actual);
        }
        byte[] binary = windows ? fromZip(archive) : fromTarGz(archive);
        if (!sha256(binary).equals(BINARY_CHECKSUMS.get(platform))) {
          throw new IllegalStateException("Extracted nats-server checksum mismatch for " + name);
        }
        Path tmp = Files.createTempFile(dir, "nats-server", ".tmp");
        Files.write(tmp, binary);
        if (!windows) tmp.toFile().setExecutable(true, true);
        Files.move(tmp, exe, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
        return exe;
      }
    } catch (IOException e) {
      throw new UncheckedIOException("Cannot install nats-server into " + dir, e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException(e);
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  private static String sha256(byte[] data) throws NoSuchAlgorithmException {
    return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(data));
  }

  /** A cached binary that does not match the pinned hash (tampered, truncated) is replaced. */
  static boolean binaryMatches(Path exe, String platform) {
    try {
      return sha256(Files.readAllBytes(exe)).equals(BINARY_CHECKSUMS.get(platform));
    } catch (IOException e) {
      return false;
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  private static byte[] download(URI uri) throws IOException, InterruptedException {
    try (HttpClient client =
        HttpClient.newBuilder()
            .followRedirects(HttpClient.Redirect.NORMAL)
            .connectTimeout(Duration.ofSeconds(30))
            .build()) {
      HttpResponse<byte[]> response =
          client.send(
              HttpRequest.newBuilder(uri).timeout(Duration.ofMinutes(5)).GET().build(),
              HttpResponse.BodyHandlers.ofByteArray());
      if (response.statusCode() != 200) {
        throw new IOException("Download of " + uri + " failed: HTTP " + response.statusCode());
      }
      return response.body();
    }
  }

  private static byte[] fromZip(byte[] archive) throws IOException {
    try (ZipInputStream zip = new ZipInputStream(new java.io.ByteArrayInputStream(archive))) {
      for (var entry = zip.getNextEntry(); entry != null; entry = zip.getNextEntry()) {
        if (entry.getName().endsWith("/nats-server.exe")) return zip.readAllBytes();
      }
    }
    throw new IOException("nats-server.exe not found in archive");
  }

  /** Minimal ustar reader: finds the {@code nats-server} regular file. */
  private static byte[] fromTarGz(byte[] archive) throws IOException {
    try (InputStream in = new GZIPInputStream(new java.io.ByteArrayInputStream(archive))) {
      byte[] header = new byte[512];
      while (in.readNBytes(header, 0, 512) == 512) {
        String name = cString(header, 0, 100);
        if (name.isEmpty()) break;
        String prefix = cString(header, 345, 155);
        String full = prefix.isEmpty() ? name : prefix + "/" + name;
        String sizeField = cString(header, 124, 12).trim();
        long size = sizeField.isEmpty() ? 0 : Long.parseLong(sizeField, 8);
        byte type = header[156];
        byte[] content = in.readNBytes((int) size);
        long padding = (512 - size % 512) % 512;
        in.readNBytes((int) padding);
        if ((type == '0' || type == 0) && full.endsWith("/nats-server")) return content;
      }
    }
    throw new IOException("nats-server not found in archive");
  }

  private static String cString(byte[] b, int offset, int length) {
    int end = offset;
    while (end < offset + length && b[end] != 0) end++;
    return new String(b, offset, end - offset, StandardCharsets.US_ASCII);
  }

  private static void deleteQuietly(Path dir) {
    try (Stream<Path> paths = Files.walk(dir)) {
      List<Path> all = paths.sorted(Comparator.reverseOrder()).toList();
      for (Path p : all) Files.deleteIfExists(p);
    } catch (IOException ignored) {
      // temp files are best effort
    }
  }
}
