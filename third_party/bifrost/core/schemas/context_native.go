package schemas

import "github.com/valyala/fasthttp"

func isNonCancellingContext(parent any) bool {
	_, ok := parent.(*fasthttp.RequestCtx)
	return ok
}
