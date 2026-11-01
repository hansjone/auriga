// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package utils

import schemas "github.com/maximhq/bifrost/core/schemas"

type BodySigner func(jsonData []byte) (map[string]string, *schemas.BifrostError)
