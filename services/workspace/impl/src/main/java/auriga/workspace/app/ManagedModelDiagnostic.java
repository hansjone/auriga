package auriga.workspace.app;

import auriga.common.observability.SensitiveDataMasker;
import java.io.PrintWriter;
import java.io.StringWriter;
import java.util.Map;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;

/** Diagnostics only: never attach an unsanitized Throwable to the logging event. */
final class ManagedModelDiagnostic {
  private ManagedModelDiagnostic() {}

  static String safe(Throwable failure, String credential) {
    var text = new StringWriter();
    if (failure.getClass().getName().startsWith("tools.jackson.")) {
      // Parser messages can quote the directory body; retain type and frames.
      text.append(failure.getClass().getName()).append(": invalid catalog JSON (body omitted)\n");
      for (var frame : failure.getStackTrace())
        text.append("\tat ").append(frame.toString()).append('\n');
    } else {
      failure.printStackTrace(new PrintWriter(text));
    }
    String raw = text.toString();
    if (credential != null && !credential.isEmpty()) raw = raw.replace(credential, "****");
    raw = raw.replaceAll("\\bamc\\.[A-Za-z0-9_.-]+", "****");
    // Reuse the common quoted-field scanner without changing common.
    raw = raw.replaceAll("(?i)\\bticket(?=[\\s\\\"']*[:=])", "auriga_diagnostic_ticket_secret");
    return SensitiveDataMasker.mask(raw).replace("auriga_diagnostic_ticket_secret", "ticket");
  }

  static void failure(
      Map<String, Object> row,
      String stage,
      String reason,
      int status,
      long started,
      Throwable failure,
      String credential) {
    failure(row, stage, reason, status, started, failure, credential, "");
  }

  static void failure(
      Map<String, Object> row,
      String stage,
      String reason,
      int status,
      long started,
      Throwable failure,
      String credential,
      String upstreamTrace) {
    LoggerFactory.getLogger(WorkspaceManagedModels.class)
        .warn(
            "Managed model provisioning deferred instance={} managedCredentialVersion={} generation={} stage={} reason={} http_status={} elapsed_ms={} trace={} upstream_trace={} exception={}",
            row.get("id"),
            row.get("managed_credential_version"),
            row.get("generation"),
            stage,
            reason,
            status,
            (System.nanoTime() - started) / 1_000_000,
            MDC.get("traceId"),
            upstreamTrace,
            safe(failure, credential));
  }
}
