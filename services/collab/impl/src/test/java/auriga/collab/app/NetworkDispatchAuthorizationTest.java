package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.NetworkAuthority;
import auriga.iam.api.NetworkAuthority.*;
import java.time.*;
import java.util.*;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class NetworkDispatchAuthorizationTest {
  final JdbcTemplate jdbc = mock(JdbcTemplate.class);
  final NetworkAuthority iam = mock(NetworkAuthority.class);
  final NetworkSourceAuthority authority = mock(NetworkSourceAuthority.class);
  final Instant now = Instant.parse("2026-10-25T00:00:00Z");
  final UUID tenant = UUID.randomUUID();

  List<NetworkMailboxService.FetchItem> items() {
    var result = new ArrayList<NetworkMailboxService.FetchItem>();
    for (int i = 1; i <= 2; i++)
      result.add(
          new NetworkMailboxService.FetchItem(
              UUID.randomUUID(),
              i,
              UUID.randomUUID(),
              "hello",
              "message",
              "10",
              "1",
              null,
              now,
              List.of(
                  new NetworkMailboxService.RecipientView(UUID.randomUUID(), "1", "pending", 1))));
    return result;
  }

  NetworkDispatchAuthorization checker(boolean differentLocal) {
    Map<UUID, String> locals = new HashMap<>();
    when(jdbc.queryForMap(anyString(), eq(tenant), any(UUID.class)))
        .thenAnswer(
            i -> {
              var message =
                  NetworkJson.object()
                      .put("message_id", i.getArgument(2).toString())
                      .put("expires_at", now.plusSeconds(600).toString())
                      .put("sender_kind", "agent")
                      .put("message_class", "unicast");
              message
                  .putObject("from")
                  .put("realm", "2")
                  .put("node", "10")
                  .put(
                      "local",
                      differentLocal
                          ? locals.computeIfAbsent(
                              i.getArgument(2), k -> Integer.toString(locals.size() + 1))
                          : "1");
              message.putObject("body").put("text", "hello");
              return Map.of(
                  "message_json",
                  NetworkJson.canonical(message),
                  "source_context_json",
                  "{\"user\":\"alice\"}");
            });
    when(iam.activeBatch(anyList())).thenReturn(Map.of(new NodeReference("2", "10"), true));
    when(iam.check(any())).thenReturn(new Decision(true, "allowed", Map.of()));
    when(authority.authorizeDelivery(eq(tenant), anyString(), anyString(), eq("20")))
        .thenReturn(now.plusSeconds(600));
    return new NetworkDispatchAuthorization(
        jdbc,
        new NetworkIamChecks(iam, new TenantTransactionManager(mock(DataSource.class))),
        authority,
        Clock.fixed(now, ZoneOffset.UTC));
  }

  @Test
  void batchesSourcesAndCachesOnlyIdenticalIamChecksWithinThisPage() {
    var check = checker(false);
    var items = items();
    var result = check.check(tenant, check.prepare(tenant, items, "2", "20"), "2", "20");
    assertEquals(2, result.size());
    assertTrue(
        result.values().stream().allMatch(o -> o.failure() == null && o.deadline().isAfter(now)));
    verify(iam).activeBatch(List.of(new NodeReference("2", "10")));
    verify(iam).check(any());
    verify(authority, times(2)).authorizeDelivery(eq(tenant), anyString(), anyString(), eq("20"));
    check.check(tenant, check.prepare(tenant, items, "2", "20"), "2", "20");
    verify(iam, times(2)).check(any()); // A new page must perform a fresh decision.
  }

  @Test
  void differentLocalsNeverShareGrantsAndBatchFailureFailsClosed() {
    var check = checker(true);
    assertTrue(
        check.check(tenant, check.prepare(tenant, items(), "2", "20"), "2", "20").values().stream()
            .allMatch(o -> o.failure() == null));
    verify(iam, times(2)).check(any());
    when(iam.activeBatch(anyList())).thenReturn(Map.of());
    assertTrue(
        check.check(tenant, check.prepare(tenant, items(), "2", "20"), "2", "20").values().stream()
            .allMatch(o -> o.failure() != null));
    verify(iam, times(2)).check(any());
  }
}
