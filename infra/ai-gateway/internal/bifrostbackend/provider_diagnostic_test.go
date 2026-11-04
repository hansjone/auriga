package bifrostbackend

import (
	"github.com/maximhq/bifrost/core/schemas"
	"testing"
)

func TestProviderFailureProjectsOnlyEnumeratedDiagnostics(t *testing.T) {
	f := &schemas.BifrostError{StatusCode: schemas.Ptr(400), Error: &schemas.ErrorField{Code: schemas.Ptr("unsupported_parameter"), Param: "temperature", Message: "secret fixture is never retained"}}
	e := providerFailure(f)
	if e.Status != 400 || e.Reason != "unsupported_parameter" || e.Parameter != "temperature" {
		t.Fatalf("diagnostic=%+v", e)
	}
	f.Error.Code = schemas.Ptr("secret-fixture")
	f.Error.Param = "secret-fixture"
	e = providerFailure(f)
	if e.Reason != "other" || e.Parameter != "other" || e.Error() != "provider_rejected_request" {
		t.Fatalf("unrecognized diagnostic escaped: %+v", e)
	}
}
