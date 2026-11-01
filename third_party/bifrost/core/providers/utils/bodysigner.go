package utils

import schemas "github.com/maximhq/bifrost/core/schemas"

type BodySigner func(jsonData []byte) (map[string]string, *schemas.BifrostError)
