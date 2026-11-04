package bifrostbackend

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
)

type listPage struct {
	After string
	Limit int
}

func pageFor(pages []listPage) listPage {
	if len(pages) > 0 {
		return pages[0]
	}
	return listPage{Limit: 200}
}
func listCursor(tenant, resource, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte("relay-list-v1|" + tenant + "|" + resource + "|" + id))
}
func parseListPage(r *http.Request, tenant, resource string) (listPage, error) {
	p := listPage{Limit: 200}
	q, e := urlQuery(r)
	if e != nil {
		return p, e
	}
	for key := range q {
		if key != "limit" && key != "cursor" {
			return p, invalid
		}
	}
	if q["limit"] != "" {
		p.Limit, e = strconv.Atoi(q["limit"])
		if e != nil || p.Limit < 1 || p.Limit > 200 {
			return p, invalid
		}
	}
	if cursor := q["cursor"]; cursor != "" {
		raw, e := base64.RawURLEncoding.Strict().DecodeString(cursor)
		parts := strings.Split(string(raw), "|")
		if e != nil || base64.RawURLEncoding.EncodeToString(raw) != cursor || len(parts) != 4 || parts[0] != "relay-list-v1" || parts[1] != tenant || parts[2] != resource {
			return p, invalid
		}
		p.After = parts[3]
		if resource == "pools" {
			if !modelID.MatchString(p.After) {
				return p, invalid
			}
		} else if !validUUID(p.After) {
			return p, invalid
		}
	}
	return p, nil
}
func afterUUID(value string) string {
	if value == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return value
}
