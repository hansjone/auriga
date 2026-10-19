package io.github.hansjone.auriga.architecture;

import static org.junit.jupiter.api.Assertions.*;

import com.tngtech.archunit.core.importer.ClassFileImporter;
import com.tngtech.archunit.core.importer.ImportOption;
import org.junit.jupiter.api.Test;

class ServiceBoundariesTest {
  @Test
  void allProductionModulesRespectBoundaries() {
    var classes =
        new ClassFileImporter()
            .withImportOption(ImportOption.Predefined.DO_NOT_INCLUDE_TESTS)
            .importPackages("io.github.hansjone.auriga");
    for (String service :
        new String[] {
          "iam", "org", "workspace", "resource", "knowledge", "collab", "audit", "ops"
        }) {
      assertTrue(
          classes.contain(
              "io.github.hansjone.auriga."
                  + service
                  + "."
                  + Character.toUpperCase(service.charAt(0))
                  + service.substring(1)
                  + "ModuleConfiguration"));
    }
    assertTrue(classes.contain("io.github.hansjone.auriga.platform.PlatformApplication"));
    assertTrue(classes.contain("io.github.hansjone.auriga.contracts.iam.v1.Session"));
    ServiceBoundaries.rule("io.github.hansjone.auriga").check(classes);
  }

  @Test
  void rejectsEveryKindOfForbiddenDependency() {
    String root = "fixtures.forbidden";
    var classes = new ClassFileImporter().importPackages(root);
    var result = ServiceBoundaries.rule(root).evaluate(classes);
    assertTrue(result.hasViolation());
    String details = String.join("\n", result.getFailureReport().getDetails());
    for (String fixture :
        new String[] {
          "org.UsesIamImpl",
          "iam.api.UsesOwnImpl",
          "org.api.UsesIamImpl",
          "common.UsesApi",
          "contracts.UsesApi",
          "iam.web.model.UsesApi"
        }) {
      assertTrue(details.contains(root + "." + fixture), fixture + " was not rejected: " + details);
    }
  }

  @Test
  void permitsOwnImplementationAndCrossServiceApis() {
    var classes = new ClassFileImporter().importPackages("fixtures.allowed");
    ServiceBoundaries.rule("fixtures.allowed").check(classes);
  }
}
