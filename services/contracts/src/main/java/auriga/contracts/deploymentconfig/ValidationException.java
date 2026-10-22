package auriga.contracts.deploymentconfig;

/** Startup configuration failure. Messages never include configuration values. */
public final class ValidationException extends IllegalArgumentException {
  private final String field;

  ValidationException(String field, String message) {
    super("common.validation_failed " + field + ": " + message);
    this.field = field;
  }

  public String code() {
    return "common.validation_failed";
  }

  public String field() {
    return field;
  }
}
