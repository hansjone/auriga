// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

import "github.com/valyala/fasthttp"

func isNonCancellingContext(parent any) bool {
	_, ok := parent.(*fasthttp.RequestCtx)
	return ok
}
