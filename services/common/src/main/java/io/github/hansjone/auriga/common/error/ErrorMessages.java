package io.github.hansjone.auriga.common.error;

import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.springframework.context.NoSuchMessageException;
import org.springframework.context.support.ResourceBundleMessageSource;

/**
 * Localized error texts. Bundles are UTF-8 properties files keyed by error code; the default file
 * is English, {@code _zh_CN} is Chinese. Placeholders use parameter names: {@code {permission}}.
 */
public final class ErrorMessages {
  public static final String COMMON_BUNDLE = "auriga/messages/common-errors";
  public static final List<Locale> SUPPORTED =
      List.of(Locale.forLanguageTag("zh-CN"), Locale.ENGLISH);
  private static final Pattern PLACEHOLDER = Pattern.compile("\\{([A-Za-z0-9_]+)}");

  private final ResourceBundleMessageSource source = new ResourceBundleMessageSource();
  private final Locale defaultLocale;

  /**
   * @param extraBundles service bundles (e.g. {@code messages/org-errors}), searched before common
   * @param defaultLocale used when Accept-Language matches nothing supported
   */
  public ErrorMessages(List<String> extraBundles, Locale defaultLocale) {
    List<String> names = new ArrayList<>(extraBundles == null ? List.of() : extraBundles);
    names.add(COMMON_BUNDLE);
    source.setBasenames(names.toArray(String[]::new));
    source.setDefaultEncoding("UTF-8");
    source.setFallbackToSystemLocale(false);
    source.setAlwaysUseMessageFormat(false);
    this.defaultLocale = defaultLocale == null ? Locale.ENGLISH : defaultLocale;
  }

  /** Picks zh-CN or en from an Accept-Language header (basic filtering, RFC 4647). */
  public Locale resolve(String acceptLanguage) {
    if (acceptLanguage == null || acceptLanguage.isBlank()) return defaultLocale;
    try {
      List<Locale> matches = Locale.filter(Locale.LanguageRange.parse(acceptLanguage), SUPPORTED);
      return matches.isEmpty() ? defaultLocale : matches.getFirst();
    } catch (IllegalArgumentException e) {
      return defaultLocale;
    }
  }

  public String message(ErrorCode code, Map<String, ?> params, Locale locale) {
    String pattern;
    try {
      pattern = source.getMessage(code.code(), null, locale);
    } catch (NoSuchMessageException e) {
      return code.code();
    }
    Matcher m = PLACEHOLDER.matcher(pattern);
    StringBuilder out = new StringBuilder();
    while (m.find()) {
      Object value = params == null ? null : params.get(m.group(1));
      m.appendReplacement(
          out, Matcher.quoteReplacement(value == null ? m.group() : value.toString()));
    }
    m.appendTail(out);
    return out.toString();
  }
}
