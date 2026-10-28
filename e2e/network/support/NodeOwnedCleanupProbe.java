import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Comparator;

/** Mirrors the Java host's Files.walk cleanup, confined to a tiny owned fixture. */
public final class NodeOwnedCleanupProbe {
  public static void main(String[] args) throws Exception {
    if (args.length != 2) throw new IllegalArgumentException("root and owner required");
    var root = Path.of(args[0]).toAbsolutePath().normalize();
    var owner = Path.of(args[1]).toAbsolutePath().normalize();
    if (!root.getParent().equals(owner)) throw new AssertionError("fixture ownership");
    try (var paths = Files.walk(root)) {
      for (var path : paths.sorted(Comparator.reverseOrder()).toList()) Files.deleteIfExists(path);
    }
    System.out.println("owned Java root removed");
  }
}
