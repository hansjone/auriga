package openai

import (
	"net/http"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
)

func ErrorInSuccessfulChatBody(body []byte) *schemas.BifrostError {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil
	}
	return errorInValidatedChatBody(body)
}

func errorInValidatedChatBody(body []byte) *schemas.BifrostError {
	errObj := gjson.GetBytes(body, "error")
	if !errObj.Exists() || !errObj.IsObject() {
		return nil
	}
	message := errObj.Get("message").String()
	if message == "" {
		return nil
	}

	statusCode := http.StatusBadGateway
	code := errObj.Get("code")
	if code.Type == gjson.Number {
		if n := int(code.Int()); n >= 400 && n <= 599 {
			statusCode = n
		}
	}

	errField := &schemas.ErrorField{Message: message}
	if code.Exists() && code.String() != "" {
		errField.Code = schemas.Ptr(code.String())
	}
	if errType := errObj.Get("metadata.error_type"); errType.Exists() && errType.String() != "" {
		errField.Type = schemas.Ptr(errType.String())
	} else if errType := errObj.Get("type"); errType.Exists() && errType.String() != "" {
		errField.Type = schemas.Ptr(errType.String())
	}

	return &schemas.BifrostError{

		IsBifrostError: false,
		StatusCode:     schemas.Ptr(statusCode),
		Error:          errField,
	}
}
