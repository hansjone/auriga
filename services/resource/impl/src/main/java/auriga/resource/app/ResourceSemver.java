package auriga.resource.app;

import java.math.BigInteger;
import java.util.regex.Pattern;

/** SemVer precedence, independent from the signed release-sequence high water. */
public final class ResourceSemver {
  private static final Pattern FORMAT =
      Pattern.compile(
          "(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*))?(?:\\+[0-9A-Za-z-]+(?:\\.[0-9A-Za-z-]+)*)?");

  private ResourceSemver() {}

  public static boolean valid(String value) {
    var match = FORMAT.matcher(value);
    if (!match.matches()) return false;
    if (match.group(4) != null)
      for (String part : match.group(4).split("\\."))
        if (part.matches("[0-9]+") && part.length() > 1 && part.startsWith("0")) return false;
    return true;
  }

  public static int compare(String left, String right) {
    if (!valid(left) || !valid(right)) throw new IllegalArgumentException("Invalid SemVer");
    var a = FORMAT.matcher(left);
    var b = FORMAT.matcher(right);
    a.matches();
    b.matches();
    for (int i = 1; i <= 3; i++) {
      int order = new BigInteger(a.group(i)).compareTo(new BigInteger(b.group(i)));
      if (order != 0) return order;
    }
    String ap = a.group(4), bp = b.group(4);
    if (ap == null || bp == null) return ap == null ? bp == null ? 0 : 1 : -1;
    var as = ap.split("\\.");
    var bs = bp.split("\\.");
    for (int i = 0; i < Math.min(as.length, bs.length); i++) {
      boolean an = as[i].matches("[0-9]+"), bn = bs[i].matches("[0-9]+");
      int order =
          an && bn
              ? new BigInteger(as[i]).compareTo(new BigInteger(bs[i]))
              : an != bn ? an ? -1 : 1 : as[i].compareTo(bs[i]);
      if (order != 0) return order;
    }
    return Integer.compare(as.length, bs.length);
  }
}
