package auriga.collab.network.group;

import auriga.collab.api.network.group.NetworkGroups.*;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import io.grpc.Status;
import java.math.BigInteger;
import java.text.Normalizer;
import java.util.*;
import java.util.Locale;

final class GroupRules {
  private GroupRules() {}

  static void require(boolean valid, String code) {
    if (!valid) throw error(code);
  }

  static AurigaException error(String code) {
    var grpc =
        switch (code) {
          case "quota_exceeded" -> Status.Code.RESOURCE_EXHAUSTED;
          case "conflict" -> Status.Code.ABORTED;
          case "contact_expired", "requires_paged_dispatch" -> Status.Code.FAILED_PRECONDITION;
          default -> Status.Code.PERMISSION_DENIED;
        };
    int http =
        switch (code) {
          case "quota_exceeded" -> 429;
          case "conflict", "requires_paged_dispatch" -> 409;
          case "contact_expired" -> 410;
          default -> 403;
        };
    return new AurigaException(ErrorCode.of("network." + code, http, grpc));
  }

  static Map<String, List<Recipient>> nodes(
      String realm, List<Recipient> targets, boolean broadcast) {
    require(!targets.isEmpty() && targets.size() <= (broadcast ? 10000 : 1024), "quota_exceeded");
    var result = new TreeMap<String, List<Recipient>>(Comparator.comparing(BigInteger::new));
    var unique = new HashSet<String>();
    for (var target : targets) {
      var a = target.address();
      address(a);
      input(realm.equals(a.realm()) && (!broadcast || a.local().equals("0")));
      require(unique.add(a.node() + "." + a.local()), "conflict");
      result.computeIfAbsent(a.node(), key -> new ArrayList<>()).add(target);
    }
    require(
        result.size() <= (broadcast ? 10000 : 256)
            && result.values().stream().allMatch(list -> list.size() <= 256),
        "quota_exceeded");
    return result;
  }

  static void atomic(Mode mode, int nodes, int locals) {
    if (mode == Mode.ATOMIC) require(nodes <= 32 && locals <= 256, "requires_paged_dispatch");
  }

  static void input(boolean valid) {
    if (!valid) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  static String decimal(String value, boolean zero) {
    input(value != null && value.matches(zero ? "0|[1-9][0-9]{0,19}" : "[1-9][0-9]{0,19}"));
    input(new BigInteger(value).bitLength() <= 64);
    return value;
  }

  static void address(Address a) {
    input(a != null);
    decimal(a.realm(), false);
    decimal(a.node(), false);
    decimal(a.local(), true);
    input(
        a.local().equals("0")
            ? "system".equals(a.type())
            : "session".equals(a.type()) || "subagent".equals(a.type()));
  }

  static void actor(Actor a) {
    require(
        a != null
            && a.sender() != null
            && a.user() != null
            && !a.user().isBlank()
            && a.binding() != null
            && !a.binding().isBlank(),
        "sender_mismatch");
    address(a.address());
    require((a.sender() == Sender.HUMAN) == a.address().local().equals("0"), "sender_mismatch");
    label(a.label());
  }

  static void label(String value) {
    input(value != null && !value.isBlank() && value.codePointCount(0, value.length()) <= 64);
    StringBuilder clean = new StringBuilder();
    Normalizer.normalize(value, Normalizer.Form.NFKC)
        .codePoints()
        .filter(
            c -> !Character.isIdentifierIgnorable(c) && Character.getType(c) != Character.FORMAT)
        .forEach(clean::appendCodePoint);
    String folded = clean.toString().toLowerCase(Locale.ROOT);
    for (String reserved : new String[] {"总部", "管理员", "系统", "admin", "system", "headquarters"})
      require(!folded.contains(reserved), "reserved_display_name");
  }
}
