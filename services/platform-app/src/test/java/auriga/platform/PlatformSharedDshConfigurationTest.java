package auriga.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.contracts.node.v1.SharedDshRuntimeIntent;
import auriga.contracts.org.v1.Member;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.org.v1.MemberType;
import auriga.iam.IamStore;
import auriga.iam.RequestIdentity;
import auriga.iam.Secrets;
import auriga.iam.SessionManager;
import auriga.iam.api.PlacementAuthorization;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.api.SharedDshAuthority.ResourceInputs;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.SharedDshAdmin;
import auriga.workspace.app.SharedDshPlacements;
import auriga.workspace.app.SharedDshTickets;
import auriga.workspace.grpc.SharedDshGrpc;
import auriga.workspace.persistence.WorkspaceStore;
import auriga.workspace.web.SharedDshController;
import jakarta.servlet.http.Cookie;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyPairGenerator;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.security.core.context.SecurityContextHolder;
import tools.jackson.databind.node.JsonNodeFactory;
import tools.jackson.databind.node.ObjectNode;

class PlatformSharedDshConfigurationTest {
  private static final String ENABLED = "auriga.workspace.shared-dsh.enabled=true";
  private static final String DIGEST = "sha256:" + "a".repeat(64);
  private final Clock clock = Clock.fixed(Instant.ofEpochSecond(10_000), ZoneOffset.UTC);
  private final WorkspaceInputs org = mock(WorkspaceInputs.class);
  private final IamStore iam = mock(IamStore.class);
  private final SessionManager sessions = mock(SessionManager.class);
  private final PlacementAuthorization authorization = mock(PlacementAuthorization.class);
  private final ResourceInputs resources = mock(ResourceInputs.class);
  private final SharedDshTickets.Entry exchange = mock(SharedDshTickets.Entry.class);
  private final SharedDshPlacements.EntryProbe ingress = mock(SharedDshPlacements.EntryProbe.class);

  private ApplicationContextRunner context() {
    return new ApplicationContextRunner()
        .withUserConfiguration(
            PlatformSharedDshConfiguration.class,
            auriga.workspace.config.WorkspaceSharedDshConfiguration.class,
            SharedDshController.class)
        .withPropertyValues(
            "auriga.platform.combined=true", "auriga.services.workspace.enabled=true");
  }

  @Test
  void normalCombinedStartupDoesNotMountSharedDsh() {
    context()
        .run(
            ctx ->
                assertThat(ctx)
                    .hasNotFailed()
                    .doesNotHaveBean(Inputs.class)
                    .doesNotHaveBean(SharedDshController.class)
                    .doesNotHaveBean(SharedDshGrpc.class));
  }

  @Test
  void optInRequiresRealResourceAndEntryDependencies() throws Exception {
    var dependencies = dependencies(mock(WorkspaceStore.class), browser(fixture()));
    dependencies
        .withPropertyValues(ENABLED)
        .run(
            ctx -> {
              assertThat(ctx).hasFailed();
              assertThat(ctx.getStartupFailure())
                  .hasStackTraceContaining(ResourceInputs.class.getName());
            });
    dependencies
        .withPropertyValues(ENABLED)
        .withBean(ResourceInputs.class, () -> resources)
        .run(
            ctx -> {
              assertThat(ctx).hasFailed();
              assertThat(ctx.getStartupFailure())
                  .hasStackTraceContaining(SharedDshPlacements.EntryProbe.class.getName());
            });
  }

  @Test
  void registeredControllerUsesLiveBrowserAndMemberAuthorityAndQueuesTheSamePoolForTwoMembers()
      throws Exception {
    var access = fixture();
    UUID tenant = UUID.fromString(access.tenantId());
    UUID alice = UUID.randomUUID(), bob = UUID.randomUUID();
    UUID aliceSession = UUID.randomUUID(), bobSession = UUID.randomUUID();
    var fixture = new PendingAllocations(tenant);
    var csrf = Secrets.random();
    var aliceRow = row(tenant, alice, aliceSession, csrf);
    var bobRow = row(tenant, bob, bobSession, csrf);
    for (var member : List.of(alice, bob))
      when(org.member(tenant, member)).thenReturn(member(tenant, member));
    when(resources.authorizationSetDigest(eq(tenant), any())).thenReturn(DIGEST);
    when(sessions.activeById(aliceSession.toString())).thenReturn(aliceRow);
    when(sessions.activeById(bobSession.toString())).thenReturn(bobRow);
    when(sessions.activeByCookie("alice-cookie", "portal")).thenReturn(aliceRow);
    when(sessions.activeByCookie("bob-cookie", "portal")).thenReturn(bobRow);
    complete(fixture.store, browser(access))
        .run(
            ctx -> {
              assertThat(ctx)
                  .hasNotFailed()
                  .hasSingleBean(SharedDshPlacements.class)
                  .hasSingleBean(SharedDshTickets.class)
                  .hasSingleBean(SharedDshAdmin.class)
                  .hasSingleBean(SharedDshGrpc.class);
              var controller = ctx.getBean(SharedDshController.class);
              var aliceRequest = request(access, "alice-cookie", csrf, access.portalUrl());
              var bobRequest = request(access, "bob-cookie", csrf, access.portalUrl());
              var alicePrincipal = principal(tenant, alice, aliceSession, TokenContext.PORTAL);
              var bobPrincipal = principal(tenant, bob, bobSession, TokenContext.PORTAL);
              calling(
                  alicePrincipal,
                  () -> {
                    failure(
                        CommonErrors.UNAUTHENTICATED,
                        () -> controller.open("wrong-cookie", bobRequest));
                    failure(
                        CommonErrors.FORBIDDEN,
                        () ->
                            controller.open(
                                "wrong-origin",
                                request(access, "alice-cookie", csrf, access.adminUrl())));
                    failure(
                        CommonErrors.FORBIDDEN,
                        () ->
                            controller.open(
                                "wrong-csrf",
                                request(
                                    access, "alice-cookie", Secrets.random(), access.portalUrl())));
                    var wrongHop = request(access, "alice-cookie", csrf, access.portalUrl());
                    wrongHop.setRemoteAddr("192.0.2.10");
                    failure(CommonErrors.FORBIDDEN, () -> controller.open("wrong-hop", wrongHop));
                    assertTrue(fixture.commands.isEmpty());
                    failure(
                        CommonErrors.CONFLICT, () -> controller.open("alice-first", aliceRequest));
                  });
              calling(
                  bobPrincipal,
                  () -> {
                    failure(CommonErrors.CONFLICT, () -> controller.open("bob-first", bobRequest));
                    failure(CommonErrors.CONFLICT, () -> controller.open("bob-reopen", bobRequest));
                  });
              assertEquals(1, fixture.pools.size());
              assertEquals(2, fixture.commands.size());
              assertEquals(
                  List.of(alice.toString(), bob.toString()),
                  fixture.commands.stream().map(i -> i.getBinding().getMemberId()).toList());
              for (var intent : fixture.commands) {
                assertEquals("prepare-empty", intent.getAction());
                assertEquals(tenant.toString(), intent.getBinding().getTenantId());
                assertEquals(
                    fixture.pools.getFirst().get("id").toString(),
                    intent.getBinding().getPhysicalInstanceId());
              }
              verifyNoInteractions(exchange, ingress);
            });
  }

  @Test
  void inputsRejectWrongTenantDeviceRevokedSessionAndStaleAdminAuthority() throws Exception {
    var access = fixture();
    UUID tenant = UUID.fromString(access.tenantId()), member = UUID.randomUUID();
    UUID session = UUID.randomUUID(), other = UUID.randomUUID();
    when(org.member(tenant, member)).thenReturn(member(tenant, member));
    var row = row(tenant, member, session, Secrets.random());
    when(sessions.activeById(session.toString())).thenReturn(row);
    complete(mock(WorkspaceStore.class), browser(access))
        .run(
            ctx -> {
              var inputs = ctx.getBean(Inputs.class);
              var portal = principal(tenant, member, session, TokenContext.PORTAL);
              calling(
                  portal,
                  () -> {
                    inputs.requireMember(tenant, member);
                    inputs.requireSession(tenant, member, session);
                    failure(CommonErrors.FORBIDDEN, () -> inputs.requireMember(other, member));
                    failure(
                        CommonErrors.UNAUTHENTICATED,
                        () -> inputs.requireSession(tenant, other, session));
                    row.put("kind", "managed_device");
                    failure(
                        CommonErrors.UNAUTHENTICATED,
                        () -> inputs.requireSession(tenant, member, session));
                    row.remove("kind");
                    when(sessions.activeById(session.toString())).thenReturn(null);
                    failure(
                        CommonErrors.UNAUTHENTICATED,
                        () -> inputs.requireSession(tenant, member, session));
                    when(org.member(tenant, member))
                        .thenReturn(
                            member(tenant, member).toBuilder()
                                .setStatus(MemberStatus.MEMBER_STATUS_OFFBOARDED)
                                .build());
                    failure(CommonErrors.FORBIDDEN, () -> inputs.requireMember(tenant, member));
                    failure(
                        CommonErrors.FORBIDDEN,
                        () -> inputs.requirePlacementWrite(tenant, member, session, 10_000));
                  });
              calling(
                  principal(tenant, member, session, TokenContext.ADMIN),
                  () -> {
                    failure(
                        CommonErrors.FORBIDDEN,
                        () -> inputs.requirePlacementWrite(tenant, member, other, 10_000));
                    doThrow(new AurigaException(CommonErrors.STEP_UP_REQUIRED))
                        .when(authorization)
                        .requireWrite();
                    failure(
                        CommonErrors.STEP_UP_REQUIRED,
                        () -> inputs.requirePlacementWrite(tenant, member, session, 10_000));
                  });
              verify(authorization, times(1)).requireWrite();
            });
  }

  private ApplicationContextRunner dependencies(WorkspaceStore store, RequestIdentity browser) {
    when(iam.tx(any())).thenAnswer(call -> call.<Supplier<?>>getArgument(0).get());
    return context()
        .withBean(WorkspaceStore.class, () -> store)
        .withBean(Clock.class, () -> clock)
        .withBean(WorkspaceInputs.class, () -> org)
        .withBean(IamStore.class, () -> iam)
        .withBean(SessionManager.class, () -> sessions)
        .withBean(RequestIdentity.class, () -> browser)
        .withBean(PlacementAuthorization.class, () -> authorization);
  }

  private ApplicationContextRunner complete(WorkspaceStore store, RequestIdentity browser)
      throws Exception {
    var key = KeyPairGenerator.getInstance("Ed25519").generateKeyPair().getPrivate();
    return dependencies(store, browser)
        .withPropertyValues(ENABLED)
        .withBean(ResourceInputs.class, () -> resources)
        .withBean(SharedDshPlacements.EntryProbe.class, () -> ingress)
        .withBean(SharedDshTickets.Entry.class, () -> exchange)
        .withBean(
            SharedDshTickets.SigningKey.class,
            () -> new SharedDshTickets.SigningKey("fixture", key));
  }

  private static DeploymentAccess fixture() throws Exception {
    return DeploymentAccess.load(
        Files.readAllBytes(Path.of("../../contracts/deployment/fixtures/domain-proxy.json")));
  }

  private static RequestIdentity browser(DeploymentAccess fixture) {
    return new RequestIdentity(fixture, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
  }

  private static MockHttpServletRequest request(
      DeploymentAccess config, String session, String csrf, String origin) {
    var request = new MockHttpServletRequest("POST", "/api/workspace/v1/me/placement/open");
    request.setRemoteAddr("127.0.0.1");
    request.addHeader("Host", URI.create(config.portalUrl()).getRawAuthority());
    request.addHeader("X-Auriga-Entry-Origin", config.portalUrl());
    request.addHeader("X-Auriga-Entry-Role", "portal");
    request.addHeader("Origin", origin);
    request.addHeader("X-Auriga-CSRF", csrf);
    request.setCookies(
        new Cookie("__Host-auriga_session", session), new Cookie("__Host-auriga_csrf", csrf));
    return request;
  }

  private static void calling(AurigaPrincipal actor, Runnable action) {
    SecurityContextHolder.getContext().setAuthentication(new AurigaAuthentication(actor));
    try {
      TenantContext.run(actor.tenantId(), action);
    } finally {
      SecurityContextHolder.clearContext();
    }
  }

  private AurigaPrincipal principal(UUID tenant, UUID member, UUID session, TokenContext context) {
    return new AurigaPrincipal(
        member.toString(),
        tenant,
        PrincipalType.USER,
        context,
        session.toString(),
        null,
        "member",
        List.of(),
        Set.of(),
        clock.instant(),
        clock.instant(),
        "fixture");
  }

  private static ObjectNode row(UUID tenant, UUID member, UUID session, String csrf) {
    return JsonNodeFactory.instance
        .objectNode()
        .put("tenantId", tenant.toString())
        .put("accountId", member.toString())
        .put("sessionId", session.toString())
        .put("audience", "portal")
        .put("csrfHash", Secrets.hash(csrf));
  }

  private static Member member(UUID tenant, UUID member) {
    return Member.newBuilder()
        .setAccountId(member.toString())
        .setTenantId(tenant.toString())
        .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
        .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
        .build();
  }

  private static void failure(auriga.common.error.ErrorCode code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode());
  }

  /** Persist the production service's pending intents in memory. No ready probe or receipt. */
  private static final class PendingAllocations {
    final JdbcTemplate jdbc = mock(JdbcTemplate.class);
    final WorkspaceStore store =
        spy(
            new WorkspaceStore(
                jdbc, mock(auriga.common.persistence.TenantTransactionManager.class)));
    final List<Map<String, Object>> pools = new ArrayList<>();
    final Map<UUID, Map<String, Object>> members = new HashMap<>();
    final List<SharedDshRuntimeIntent> commands = new ArrayList<>();

    PendingAllocations(UUID tenant) {
      doAnswer(call -> call.<Supplier<?>>getArgument(0).get()).when(store).tx(any());
      doNothing().when(store).lock();
      doNothing().when(store).register(tenant, "tenant", tenant);
      when(jdbc.queryForObject(
              startsWith("SELECT count(*) FROM instance"),
              eq(Long.class),
              eq(tenant),
              any(UUID.class)))
          .thenReturn(0L);
      when(jdbc.queryForList(
              startsWith("SELECT * FROM shared_dsh_placement"), eq(tenant), any(UUID.class)))
          .thenAnswer(
              call ->
                  Optional.ofNullable(members.get(call.<UUID>getArgument(2))).stream().toList());
      doAnswer(call -> members.get(call.<UUID>getArgument(2)))
          .when(store)
          .one(startsWith("SELECT * FROM shared_dsh_placement"), eq(tenant), any(UUID.class));
      when(jdbc.queryForList(
              startsWith("SELECT p.id FROM shared_dsh_pool"), eq(tenant), eq(DIGEST)))
          .thenReturn(pools);
      when(jdbc.update(
              startsWith("INSERT INTO shared_dsh_pool"),
              any(UUID.class),
              eq(tenant),
              eq("shared"),
              eq(DIGEST)))
          .thenAnswer(
              call -> {
                pools.add(Map.of("id", call.getArgument(1)));
                return 1;
              });
      when(jdbc.update(
              startsWith("INSERT INTO shared_dsh_placement"),
              any(UUID.class),
              eq(tenant),
              any(UUID.class),
              any(UUID.class)))
          .thenAnswer(
              call -> {
                UUID member = call.getArgument(3);
                members.put(
                    member,
                    Map.of(
                        "id",
                        call.getArgument(1),
                        "tenant_id",
                        tenant,
                        "member_id",
                        member,
                        "pool_id",
                        call.getArgument(4),
                        "mode",
                        "shared",
                        "state",
                        "pending",
                        "epoch",
                        1L,
                        "revision",
                        1L,
                        "auth_generation",
                        1L));
                return 1;
              });
      when(jdbc.update(
              startsWith("INSERT INTO shared_dsh_outbox"),
              any(UUID.class),
              eq(tenant),
              eq("runtime"),
              any(byte[].class),
              anyLong(),
              eq(tenant),
              eq("runtime"),
              any(byte[].class)))
          .thenAnswer(
              call -> {
                commands.add(SharedDshRuntimeIntent.parseFrom(call.<byte[]>getArgument(4)));
                return 1;
              });
    }
  }
}
