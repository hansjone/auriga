package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.workspace.web.WorkspaceController;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.aop.framework.autoproxy.DefaultAdvisorAutoProxyCreator;
import org.springframework.context.annotation.*;
import org.springframework.core.env.MapPropertySource;
import org.springframework.mock.web.MockServletContext;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;
import org.springframework.web.context.support.AnnotationConfigWebApplicationContext;
import org.springframework.web.servlet.config.annotation.EnableWebMvc;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;
import tools.jackson.databind.json.JsonMapper;

class WorkspaceAvailabilityTest {
  @Configuration(proxyBeanMethods = false)
  @EnableWebMvc
  @ComponentScan(
      basePackageClasses = WorkspaceController.class,
      useDefaultFilters = false,
      includeFilters =
          @ComponentScan.Filter(
              type = FilterType.REGEX,
              pattern = "auriga\\.workspace\\.web\\.WorkspaceAvailabilityController"))
  static class Mvc {
    @Bean
    static DefaultAdvisorAutoProxyCreator proxies() {
      var proxies = new DefaultAdvisorAutoProxyCreator();
      proxies.setProxyTargetClass(true);
      return proxies;
    }

    @Bean
    AuthorizationAdvisor authorization() {
      return new AuthorizationAdvisor(Clock.systemUTC());
    }

    @Bean
    ProblemExceptionHandler problems() {
      return new ProblemExceptionHandler(
          new Problems(new ErrorMessages(List.of(), Locale.ENGLISH)));
    }
  }

  AnnotationConfigWebApplicationContext context(Map<String, Object> properties) {
    var context = new AnnotationConfigWebApplicationContext();
    context.setServletContext(new MockServletContext());
    context
        .getEnvironment()
        .getPropertySources()
        .addFirst(new MapPropertySource("test", properties));
    context.register(Mvc.class);
    context.refresh();
    new EndpointAuthorizationCheck(context.getBeanProvider(RequestMappingHandlerMapping.class))
        .afterSingletonsInstantiated();
    return context;
  }

  @Test
  void disabledListingReturnsExplicitProblemAndRequiresAuthentication() throws Exception {
    for (var properties :
        List.of(
            Map.<String, Object>of(
                "auriga.services.workspace.enabled", false, "auriga.platform.combined", true),
            Map.<String, Object>of(
                "auriga.services.workspace.enabled",
                false,
                "auriga.service.schema",
                "workspace"))) {
      try (var context = context(properties)) {
        var mvc = MockMvcBuilders.webAppContextSetup(context).build();
        var principal =
            new AurigaPrincipal(
                "alice",
                UUID.randomUUID(),
                PrincipalType.USER,
                TokenContext.PORTAL,
                "session",
                null,
                null,
                List.of(),
                Set.of(),
                null,
                null,
                "token");
        SecurityContextHolder.getContext().setAuthentication(new AurigaAuthentication(principal));
        var response = mvc.perform(get("/api/workspace/v1/me/instances")).andReturn().getResponse();
        assertEquals(503, response.getStatus(), response.getContentAsString());
        var problem = new JsonMapper().readTree(response.getContentAsString());
        assertEquals("common.unavailable", problem.get("code").asString());
        assertEquals("workspace", problem.get("params").get("service").asString());
        assertEquals("disabled", problem.get("params").get("reason").asString());
        SecurityContextHolder.clearContext();
        response = mvc.perform(get("/api/workspace/v1/me/instances")).andReturn().getResponse();
        assertEquals(401, response.getStatus(), response.getContentAsString());
        assertEquals(
            "common.unauthenticated",
            new JsonMapper().readTree(response.getContentAsString()).get("code").asString());
      } finally {
        SecurityContextHolder.clearContext();
      }
    }
  }

  @Test
  void enabledDefaultAndOtherServiceDoNotRegisterFallback() {
    for (var properties :
        List.of(
            Map.<String, Object>of(
                "auriga.services.workspace.enabled", true, "auriga.platform.combined", true),
            Map.<String, Object>of("auriga.platform.combined", true),
            Map.<String, Object>of(
                "auriga.services.workspace.enabled", false, "auriga.service.schema", "iam"))) {
      try (var context = context(properties)) {
        assertTrue(
            context.getBean(RequestMappingHandlerMapping.class).getHandlerMethods().isEmpty());
      }
    }
  }
}
