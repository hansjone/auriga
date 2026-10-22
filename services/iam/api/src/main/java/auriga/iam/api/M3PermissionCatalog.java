package auriga.iam.api;

import java.io.IOException;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Read-only metadata from the formal contract; no client-supplied privilege flags. */
public final class M3PermissionCatalog {
  public record Permission(
      String key,
      String english,
      String chinese,
      boolean adminOnly,
      boolean sensitive,
      Set<String> builtinRoles) {}

  private static final JsonNode SOURCE = load();
  private static final Map<String, Permission> PERMISSIONS = permissions();

  private static JsonNode load() {
    try (var input =
        M3PermissionCatalog.class.getResourceAsStream("/auriga/contracts/catalog/m3.json")) {
      if (input == null) throw new IllegalStateException("Formal M3 permission catalog is missing");
      JsonNode source = JsonMapper.builder().build().readTree(input);
      if (source.path("schemaVersion").asInt() != 1 || !source.path("permissions").isObject())
        throw new IllegalStateException("Invalid M3 permission catalog");
      return source;
    } catch (IOException e) {
      throw new IllegalStateException("Cannot read formal M3 permission catalog", e);
    }
  }

  private static Map<String, Permission> permissions() {
    Map<String, Permission> result = new TreeMap<>();
    SOURCE
        .path("permissions")
        .properties()
        .forEach(
            entry -> {
              var value = entry.getValue();
              Set<String> roles = new TreeSet<>();
              value.path("builtinRoles").forEach(role -> roles.add(role.asString()));
              if (!value.path("adminOnly").isBoolean() || !value.path("sensitive").isBoolean())
                throw new IllegalStateException("Missing M3 permission security metadata");
              result.put(
                  entry.getKey(),
                  new Permission(
                      entry.getKey(),
                      value.path("description").path("en").asString(),
                      value.path("description").path("zh-CN").asString(),
                      value.path("adminOnly").asBoolean(),
                      value.path("sensitive").asBoolean(),
                      Set.copyOf(roles)));
            });
    return Map.copyOf(result);
  }

  public static Collection<Permission> all() {
    return PERMISSIONS.values().stream().sorted(Comparator.comparing(Permission::key)).toList();
  }

  public static boolean adminOnly(String key) {
    Permission permission = PERMISSIONS.get(key);
    return permission != null && permission.adminOnly();
  }

  public static Set<String> forRole(String key) {
    Set<String> result = new TreeSet<>();
    all().stream()
        .filter(permission -> permission.builtinRoles().contains(key))
        .forEach(permission -> result.add(permission.key()));
    return Set.copyOf(result);
  }

  public static Set<String> devicePermissions(String key) {
    Set<String> result = new TreeSet<>();
    SOURCE
        .path("deviceMappings")
        .path(key)
        .forEach(permission -> result.add(permission.asString()));
    return Set.copyOf(result);
  }

  private M3PermissionCatalog() {}
}
