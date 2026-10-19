package auriga.testsupport;

import org.junit.jupiter.api.extension.BeforeAllCallback;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.jupiter.api.extension.ParameterContext;
import org.junit.jupiter.api.extension.ParameterResolver;
import org.springframework.test.context.DynamicPropertyRegistry;

/**
 * One {@link NatsServer} per JUnit JVM, stopped when the root store closes. Tests share the server,
 * so they must use their own consumer names (and tolerate other tests' messages in the platform
 * streams).
 */
public final class NatsExtension implements BeforeAllCallback, ParameterResolver {
  private static final ExtensionContext.Namespace NAMESPACE =
      ExtensionContext.Namespace.create(NatsExtension.class);
  private static Holder holder;

  @Override
  public void beforeAll(ExtensionContext context) {
    context.getRoot().getStore(NAMESPACE).getOrComputeIfAbsent(Holder.class, key -> shared());
  }

  private static synchronized Holder shared() {
    if (holder == null) holder = new Holder(NatsServer.start());
    return holder;
  }

  /** Client URL of the shared server; usable from {@code @DynamicPropertySource}. */
  public static String url() {
    return shared().server.url();
  }

  public static void properties(DynamicPropertyRegistry registry) {
    registry.add("auriga.events.nats-url", NatsExtension::url);
  }

  @Override
  public boolean supportsParameter(ParameterContext parameter, ExtensionContext context) {
    return parameter.getParameter().getType() == NatsServer.class;
  }

  @Override
  public Object resolveParameter(ParameterContext parameter, ExtensionContext context) {
    return shared().server;
  }

  private static final class Holder implements ExtensionContext.Store.CloseableResource {
    private final NatsServer server;

    private Holder(NatsServer server) {
      this.server = server;
    }

    @Override
    public void close() {
      try {
        server.close();
      } finally {
        synchronized (NatsExtension.class) {
          holder = null;
        }
      }
    }
  }
}
