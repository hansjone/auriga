package auriga.architecture;

import static com.tngtech.archunit.lang.syntax.ArchRuleDefinition.classes;

import com.tngtech.archunit.core.domain.JavaClass;
import com.tngtech.archunit.lang.ArchCondition;
import com.tngtech.archunit.lang.ArchRule;
import com.tngtech.archunit.lang.ConditionEvents;
import com.tngtech.archunit.lang.SimpleConditionEvent;
import java.util.Set;

final class ServiceBoundaries {
  private static final Set<String> SERVICES =
      Set.of("iam", "org", "workspace", "resource", "knowledge", "collab", "audit", "ops");

  private ServiceBoundaries() {}

  static ArchRule rule(String root) {
    return classes()
        .should(
            new ArchCondition<JavaClass>("respect service API boundaries") {
              @Override
              public void check(JavaClass origin, ConditionEvents events) {
                for (var dependency : origin.getDirectDependenciesFromSelf()) {
                  JavaClass target = dependency.getTargetClass();
                  String from = origin.getPackageName();
                  String to = target.getPackageName();
                  String sourceService = service(root, from);
                  String targetService = service(root, to);
                  boolean sourceContracts = generated(root, from);
                  boolean sourceCommon = under(from, root + ".common");
                  boolean sourceApi =
                      sourceService != null && under(from, root + "." + sourceService + ".api");
                  boolean targetApi =
                      targetService != null && under(to, root + "." + targetService + ".api");
                  boolean forbidden =
                      targetService != null
                          && (sourceCommon
                              || sourceContracts
                              || (sourceApi && !targetApi)
                              || (sourceService != null
                                  && !sourceService.equals(targetService)
                                  && !targetApi));
                  if (forbidden)
                    events.add(SimpleConditionEvent.violated(origin, dependency.getDescription()));
                }
              }
            })
        .allowEmptyShould(false);
  }

  private static boolean under(String pkg, String prefix) {
    return pkg.equals(prefix) || pkg.startsWith(prefix + ".");
  }

  private static boolean generated(String root, String pkg) {
    if (under(pkg, root + ".contracts")) return true;
    return SERVICES.stream()
        .anyMatch(
            service ->
                under(pkg, root + "." + service + ".web.api")
                    || under(pkg, root + "." + service + ".web.model"));
  }

  private static String service(String root, String pkg) {
    if (generated(root, pkg)) return null;
    return SERVICES.stream()
        .filter(service -> under(pkg, root + "." + service))
        .findFirst()
        .orElse(null);
  }
}
