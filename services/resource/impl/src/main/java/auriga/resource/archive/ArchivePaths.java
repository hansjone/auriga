package auriga.resource.archive;

import java.text.Normalizer;
import java.util.*;

/** Comparison keys only: never rewrites archive bytes or resolves a host filesystem path. */
final class ArchivePaths {
  private final boolean windows;
  private final NavigableMap<String, Boolean> entries = new TreeMap<>();

  ArchivePaths(boolean windows) {
    this.windows = windows;
  }

  void accept(String name, boolean directory) throws java.io.IOException {
    if (name.isEmpty()
        || name.startsWith("/")
        || name.indexOf('\\') >= 0
        || (!directory && name.endsWith("/"))) throw StrictUstarInputStream.bad();
    for (int i = 0; i < name.length(); i++)
      if (Character.isISOControl(name.charAt(i))) throw StrictUstarInputStream.bad();
    var segments = new ArrayList<String>();
    for (String segment : name.split("/", -1)) {
      if (segment.equals("..")) throw StrictUstarInputStream.bad();
      if (segment.isEmpty() || segment.equals(".")) continue;
      String key = Normalizer.normalize(segment, Normalizer.Form.NFC);
      if (windows) {
        if (key.endsWith(".")
            || key.endsWith(" ")
            || key.chars().anyMatch(c -> "<>:\"|?*".indexOf(c) >= 0))
          throw StrictUstarInputStream.bad();
        key = key.toUpperCase(Locale.ROOT);
        String base = key.split("\\.", 2)[0].replaceAll("[ .]+$", "");
        if (Set.of("CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$").contains(base)
            || base.matches("(?:COM|LPT)[1-9¹²³]")) throw StrictUstarInputStream.bad();
      }
      segments.add(key);
    }
    String key = String.join("/", segments);
    if ((!directory && key.isEmpty()) || key.matches("^[A-Za-z]:.*") || entries.containsKey(key))
      throw StrictUstarInputStream.bad();
    for (int slash = key.indexOf('/'); slash >= 0; slash = key.indexOf('/', slash + 1)) {
      if (Boolean.FALSE.equals(entries.get(key.substring(0, slash))))
        throw StrictUstarInputStream.bad();
    }
    if (!directory) {
      String child = entries.ceilingKey(key + "/");
      if (child != null && child.startsWith(key + "/")) throw StrictUstarInputStream.bad();
    }
    entries.put(key, directory);
  }
}
