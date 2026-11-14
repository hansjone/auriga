package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;

import java.util.*;
import tools.jackson.databind.JsonNode;

/**
 * The frozen machine policy is the permission source; K2 ships no generated Java permission class.
 */
public final class CatalogReviewPolicy {
  private static final String PERMISSION = loadPermission();

  private CatalogReviewPolicy() {}

  private static String loadPermission() {
    try (var in =
        CatalogReviewPolicy.class
            .getClassLoader()
            .getResourceAsStream("auriga/contracts/resource/unified-authorization.json")) {
      if (in == null)
        throw new IllegalStateException("Missing frozen catalog authorization policy");
      String permission =
          JSON.readTree(in).path("securityReviewQualification").path("permission").asString();
      if (permission.isBlank())
        throw new IllegalStateException("Missing security qualification permission");
      return permission;
    } catch (java.io.IOException e) {
      throw new IllegalStateException(e);
    }
  }

  public static String permission() {
    return PERMISSION;
  }

  public static boolean valid(JsonNode input, JsonNode review, JsonNode principal) {
    if (principal == null || principal.isMissingNode()) return false;
    return principal.path("tenantId").equals(input.path("tenantId"))
        && principal.path("reviewerId").equals(review.path("reviewerId"))
        && review.path("tenantId").equals(input.path("tenantId"))
        && !review.path("reviewerId").equals(input.path("authorId"))
        && review.path("versionRevision").asLong() == input.path("versionRevision").asLong()
        && review.path("digest").equals(input.path("digest"))
        && Set.of("general", "security").contains(review.path("role").asString())
        && has(principal, "resource.versions.review")
        && (!review.path("role").asString().equals("security") || has(principal, permission()));
  }

  private static boolean has(JsonNode principal, String permission) {
    for (var p : principal.path("permissions")) if (permission.equals(p.asString())) return true;
    return false;
  }

  public record Counts(int distinctReviewers, int securityReviewers) {
    public boolean allowed(String risk) {
      return distinctReviewers >= (risk.equals("r1") ? 1 : 2)
          && (risk.equals("r1") || securityReviewers > 0);
    }
  }

  public static Counts count(JsonNode input) {
    var reviewers = new HashSet<String>();
    var security = new HashSet<String>();
    for (var review : input.path("reviews")) {
      JsonNode principal = null;
      for (var candidate : input.path("currentPrincipals"))
        if (candidate.path("reviewerId").equals(review.path("reviewerId"))
            && candidate.path("tenantId").equals(input.path("tenantId"))) {
          principal = candidate;
          break;
        }
      if (!review.path("decision").asString().equals("approve") || !valid(input, review, principal))
        continue;
      String id = review.path("reviewerId").asString();
      reviewers.add(id);
      if (review.path("role").asString().equals("security")) security.add(id);
    }
    return new Counts(reviewers.size(), security.size());
  }
}
