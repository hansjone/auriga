package io.github.hansjone.auriga.common.security;

import jakarta.servlet.ServletException;
import java.io.IOException;

/** Runs a servlet filter chain inside a {@link ScopedValue} binding without losing exceptions. */
final class ServletScopes {
  private ServletScopes() {}

  @FunctionalInterface
  interface Work {
    void run() throws IOException, ServletException;
  }

  @FunctionalInterface
  interface Binder {
    Object call(ScopedValue.CallableOp<Object, Exception> op) throws Exception;
  }

  static void run(Binder binder, Work work) throws IOException, ServletException {
    try {
      binder.call(
          () -> {
            work.run();
            return null;
          });
    } catch (IOException | ServletException | RuntimeException e) {
      throw e;
    } catch (Exception e) {
      throw new ServletException(e);
    }
  }
}
