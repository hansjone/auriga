package auriga.resource;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.resource.app.*;
import java.nio.file.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Execute the exact K2 fixture inputs against the production policies, without rewriting negatives.
 */
class CatalogK2FixturesTest {
  JsonNode read(String name) throws Exception {
    return JSON.readTree(
        Files.readAllBytes(Path.of("../../../contracts/resource/" + name + "-fixtures.json")));
  }

  void same(JsonNode expected, JsonNode actual, String id) {
    assertEquals(ResourceSigning.canonical(expected), ResourceSigning.canonical(actual), id);
  }

  @Test
  void securityReviewQualificationAndUnorderedDistinctRoles() throws Exception {
    var fixture = read("security-review");
    for (var c : fixture.path("cases")) {
      var input = c.path("input");
      var result = JSON.createObjectNode();
      boolean allowed;
      if (c.path("operation").asString().equals("review-decision"))
        allowed = CatalogReviewPolicy.valid(input, input.path("review"), input.path("principal"));
      else {
        var counts = CatalogReviewPolicy.count(input);
        allowed = counts.allowed(input.path("riskLevel").asString());
        result
            .put("distinctReviewers", counts.distinctReviewers())
            .put("securityReviewers", counts.securityReviewers());
      }
      result.put("allowed", allowed);
      if (allowed) result.putNull("errorCode");
      else
        result.put(
            "errorCode",
            c.path("operation").asString().equals("review-decision")
                ? "common.forbidden"
                : "resource.review_required");
      same(c.path("expect"), result, c.path("id").asString());
    }
    assertEquals(19, fixture.path("cases").size());
  }

  @Test
  void installationRevisionFixtures() throws Exception {
    var fixture = read("installation-revision");
    for (var c : fixture.path("cases")) {
      var input = c.path("input");
      JsonNode result;
      switch (c.path("operation").asString()) {
        case "dispatch-validation" ->
            result = JSON.valueToTree(CatalogStatePolicy.validDispatch(input));
        case "renewal" ->
            result =
                JSON.createObjectNode()
                    .put(
                        "sameInstallationRevision",
                        input.path("before").path("installationRevision").asLong()
                            == input.path("after").path("installationRevision").asLong());
        case "mutation" -> {
          long revision = input.path("persistedRevision").asLong();
          boolean accepted = true;
          try {
            revision = CatalogStatePolicy.advance(revision, input.path("ifMatch").asLong());
          } catch (AurigaException e) {
            accepted = false;
          }
          result =
              JSON.createObjectNode()
                  .put("accepted", accepted)
                  .put("installationRevision", revision);
        }
        case "lifecycle" -> {
          long revision = input.path("initialRevision").asLong();
          var replay = new HashMap<String, Long>();
          var n = JSON.createObjectNode();
          var revisions = n.putArray("responseRevisions");
          for (var step : input.path("steps")) {
            String key = step.path("idempotencyKey").asString();
            if (replay.containsKey(key)) revisions.add(replay.get(key));
            else {
              revision = CatalogStatePolicy.advance(revision, step.path("ifMatch").asLong());
              replay.put(key, revision);
              revisions.add(revision);
            }
          }
          n.put("persistedRevision", revision);
          result = n;
        }
        case "report" -> {
          var expected = input.path("expected");
          boolean applied =
              CatalogStatePolicy.acknowledged(
                  expected.path("resource"),
                  expected.path("selectionRevision").asLong(),
                  expected.path("generation").asLong(),
                  input.path("report"),
                  input.path("channel").asString());
          var n = JSON.createObjectNode().put("applied", applied);
          if (applied) n.putNull("errorCode");
          else n.put("errorCode", CatalogStatePolicy.conflict().errorCode().code());
          result = n;
        }
        default -> throw new AssertionError("Unknown fixture operation");
      }
      same(c.path("expect"), result, c.path("id").asString());
    }
    assertEquals(27, fixture.path("cases").size());
  }

  @Test
  void desiredStateAndRemovalCollectionFixtures() throws Exception {
    var fixture = read("desired-state");
    for (var c : fixture.path("cases")) {
      var input = c.path("input");
      JsonNode result;
      var expected = input.path("expected");
      switch (c.path("operation").asString()) {
        case "desired-state" -> {
          var n = JSON.createObjectNode();
          try {
            n.put("state", CatalogStatePolicy.desired(input));
            n.putNull("errorCode");
          } catch (AurigaException e) {
            n.putNull("state");
            n.put("errorCode", e.errorCode().code());
          }
          result = n;
        }
        case "dispatch" -> {
          var before = (ObjectNode) input.path("before").deepCopy();
          result =
              CatalogStatePolicy.assign(
                  before,
                  CatalogStatePolicy.advance(
                      before.path("installationRevision").asLong(),
                      before.path("installationRevision").asLong()),
                  CatalogStatePolicy.stateForAction(input.path("action").asString()));
        }
        case "acknowledgement" -> {
          boolean accepted =
              CatalogStatePolicy.removal(
                  expected.path("resource"),
                  expected.path("selectionRevision").asLong(),
                  expected.path("generation").asLong(),
                  input.path("report"),
                  input.path("channel").asString(),
                  input.path("contentCleared").asBoolean());
          var n = JSON.createObjectNode().put("accepted", accepted);
          if (accepted) n.putNull("errorCode");
          else n.put("errorCode", CatalogStatePolicy.conflict().errorCode().code());
          result = n;
        }
        case "collect" -> {
          boolean collected =
              CatalogStatePolicy.collect(
                  expected.path("resource"),
                  expected.path("selectionRevision").asLong(),
                  expected.path("generation").asLong(),
                  input.path("report"),
                  input.path("channel").asString(),
                  input.path("contentCleared").asBoolean(),
                  input.path("nextGeneration").asLong());
          var n =
              JSON.createObjectNode()
                  .put("collected", collected)
                  .put(
                      "installationRevision",
                      expected.path("resource").path("installationRevision").asLong());
          var resources = n.putArray("resources");
          for (var r : input.path("resources"))
            if (!collected || !r.equals(expected.path("resource"))) resources.add(r);
          result = n;
        }
        case "execute" -> {
          var current = input.path("current");
          var incoming = input.path("incoming");
          var report = (ObjectNode) incoming.path("resource").deepCopy();
          report.set("selectionRevision", incoming.path("selectionRevision"));
          report.set("generation", incoming.path("generation"));
          report.put("reportSource", "node");
          boolean execute =
              CatalogStatePolicy.desired(incoming.path("resource")).equals("REMOVED")
                  && CatalogStatePolicy.desired(current.path("resource")).equals("REMOVED")
                  && CatalogStatePolicy.tuple(
                      current.path("resource"),
                      current.path("selectionRevision").asLong(),
                      current.path("generation").asLong(),
                      report,
                      "node");
          result = JSON.createObjectNode().put("execute", execute);
        }
        default -> throw new AssertionError("Unknown fixture operation");
      }
      same(c.path("expect"), result, c.path("id").asString());
    }
    assertEquals(30, fixture.path("cases").size());
  }
}
