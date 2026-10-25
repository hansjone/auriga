package auriga.collab.network.group;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.network.group.NetworkGroups.*;
import auriga.common.error.AurigaException;
import java.util.*;
import org.junit.jupiter.api.Test;

class NetworkGroupRulesTest {
  @Test
  void atomicAndPagedBoundariesAndNodeAggregation() {
    var recipients = new ArrayList<Recipient>();
    for (int i = 0; i < 100; i++)
      recipients.add(
          new Recipient(
              UUID.randomUUID(),
              UUID.randomUUID(),
              new Address("2", "" + (10 + i / 10), "" + (1 + i % 10), "session")));
    assertEquals(10, GroupRules.nodes("2", recipients, false).size());
    GroupRules.atomic(Mode.ATOMIC, 32, 256);
    GroupRules.atomic(Mode.PAGED, 33, 257);
    for (int[] size : new int[][] {{33, 256}, {32, 257}})
      assertEquals(
          "network.requires_paged_dispatch",
          assertThrows(
                  AurigaException.class, () -> GroupRules.atomic(Mode.ATOMIC, size[0], size[1]))
              .errorCode()
              .code());
    recipients.add(recipients.getFirst());
    assertEquals(
        "network.conflict",
        assertThrows(AurigaException.class, () -> GroupRules.nodes("2", recipients, false))
            .errorCode()
            .code());
  }

  @Test
  void groupBroadcastAndPerNodeHardLimits() {
    var list = new ArrayList<Recipient>();
    for (int i = 0; i < 1024; i++)
      list.add(
          new Recipient(
              UUID.randomUUID(),
              UUID.randomUUID(),
              new Address("2", "" + (1 + i / 4), "" + (1 + i % 4), "session")));
    assertEquals(256, GroupRules.nodes("2", list, false).size());
    list.add(
        new Recipient(
            UUID.randomUUID(), UUID.randomUUID(), new Address("2", "257", "1", "session")));
    assertEquals(
        "network.quota_exceeded",
        assertThrows(AurigaException.class, () -> GroupRules.nodes("2", list, false))
            .errorCode()
            .code());
    list.clear();
    for (int i = 0; i < 10000; i++)
      list.add(
          new Recipient(
              UUID.randomUUID(), UUID.randomUUID(), new Address("2", "" + (1 + i), "0", "system")));
    assertEquals(10000, GroupRules.nodes("2", list, true).size());
    list.add(
        new Recipient(
            UUID.randomUUID(), UUID.randomUUID(), new Address("2", "10001", "0", "system")));
    assertThrows(AurigaException.class, () -> GroupRules.nodes("2", list, true));
    list.clear();
    for (int i = 0; i < 257; i++)
      list.add(
          new Recipient(
              UUID.randomUUID(),
              UUID.randomUUID(),
              new Address("2", "10", "" + (i + 1), "session")));
    assertThrows(AurigaException.class, () -> GroupRules.nodes("2", list, false));
  }

  @Test
  void uint64ExtremesAndCrossRealmSnapshot() {
    assertEquals("18446744073709551615", GroupRules.decimal("18446744073709551615", false));
    for (String value : new String[] {"18446744073709551616", "01", "-1", "0", "1e2"})
      assertThrows(AurigaException.class, () -> GroupRules.decimal(value, false));
    assertThrows(
        AurigaException.class,
        () ->
            GroupRules.nodes(
                "2",
                List.of(
                    new Recipient(
                        UUID.randomUUID(),
                        UUID.randomUUID(),
                        new Address("3", "10", "0", "system"))),
                true));
  }

  @Test
  void canonicalAddressesAndHumanSources() {
    GroupRules.address(new Address("2", "10", "1", "session"));
    GroupRules.actor(
        new Actor("alice", new Address("2", "10", "0", "system"), Sender.HUMAN, "ui", "Alice"));
    assertThrows(
        AurigaException.class, () -> GroupRules.address(new Address("02", "10", "1", "session")));
    assertThrows(
        AurigaException.class, () -> GroupRules.address(new Address("2", "0", "2", "group")));
    assertThrows(
        AurigaException.class,
        () ->
            GroupRules.actor(
                new Actor(
                    "alice", new Address("2", "10", "1", "session"), Sender.HUMAN, "ui", "Alice")));
    assertThrows(
        AurigaException.class,
        () ->
            GroupRules.actor(
                new Actor(
                    "alice", new Address("2", "10", "0", "system"), Sender.AGENT, "b", "Alice")));
  }

  @Test
  void unicodeReservedNamesAndInvisibleVariants() {
    GroupRules.label("研发讨论");
    for (String name : new String[] {"总\u200b部", "ＡＤＭＩＮ", "管理员", "系\u202e统", "SYSTEM"}) {
      assertEquals(
          "network.reserved_display_name",
          assertThrows(AurigaException.class, () -> GroupRules.label(name)).errorCode().code());
    }
  }
}
