package io.github.hansjone.auriga.common.error;

import static org.junit.jupiter.api.Assertions.*;

import io.grpc.Status;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import org.junit.jupiter.api.Test;

class ErrorMessagesTest {
  private final ErrorMessages messages = new ErrorMessages(List.of("test-errors"), Locale.ENGLISH);
  private static final ErrorCode QUOTA =
      ErrorCode.of("org.quota_exceeded", 409, Status.Code.FAILED_PRECONDITION);

  @Test
  void picksLocaleFromAcceptLanguage() {
    assertEquals(Locale.forLanguageTag("zh-CN"), messages.resolve("zh-CN,zh;q=0.9,en;q=0.8"));
    assertEquals(Locale.forLanguageTag("zh-CN"), messages.resolve("zh"));
    assertEquals(Locale.ENGLISH, messages.resolve("en-US"));
    assertEquals(Locale.ENGLISH, messages.resolve("fr-FR"));
    assertEquals(Locale.ENGLISH, messages.resolve(null));
    assertEquals(Locale.ENGLISH, messages.resolve(";;;=="));
    assertEquals(Locale.forLanguageTag("zh-CN"), messages.resolve("fr;q=0.9, zh-CN;q=0.5"));
  }

  @Test
  void localizesCommonCodes() {
    assertEquals(
        "没有权限执行这个操作。",
        messages.message(CommonErrors.FORBIDDEN, Map.of(), Locale.forLanguageTag("zh-CN")));
    assertEquals(
        "You do not have permission to perform this action.",
        messages.message(CommonErrors.FORBIDDEN, Map.of(), Locale.ENGLISH));
  }

  @Test
  void serviceBundlesAndNamedParams() {
    assertEquals(
        "Quota of 5 instances exceeded.",
        messages.message(QUOTA, Map.of("limit", 5), Locale.ENGLISH));
    assertEquals(
        "超出 5 个实例的配额。",
        messages.message(QUOTA, Map.of("limit", 5), Locale.forLanguageTag("zh-CN")));
    // unknown placeholders stay visible, unknown codes fall back to the code
    assertEquals(
        "Quota of {limit} instances exceeded.", messages.message(QUOTA, Map.of(), Locale.ENGLISH));
    assertEquals(
        "x.unknown",
        messages.message(
            ErrorCode.of("x.unknown", 400, Status.Code.INVALID_ARGUMENT), null, Locale.ENGLISH));
  }

  @Test
  void errorCodeFormatIsEnforced() {
    assertThrows(
        IllegalArgumentException.class,
        () -> ErrorCode.of("Forbidden", 403, Status.Code.PERMISSION_DENIED));
    assertThrows(
        IllegalArgumentException.class,
        () -> ErrorCode.of("common.forbidden", 200, Status.Code.OK));
    assertEquals("org", QUOTA.domain());
  }

  @Test
  void fieldPointers() {
    assertEquals("/items/0/name", FieldViolation.pointer("items[0].name"));
    assertEquals("/a~1b", FieldViolation.pointer("a/b"));
  }
}
