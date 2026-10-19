package auriga.common.events;

/** An event does not match its contract (programming error on the producer side). */
public class InvalidEventException extends IllegalArgumentException {
  public InvalidEventException(String message) {
    super(message);
  }
}
