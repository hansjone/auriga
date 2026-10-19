package auriga.iam.api;

/** Installation credentials only; never exposed by the public OAuth token endpoint. */
public interface InternalTokenIssuer {
  String issue(String service, String installationSecret);
}
