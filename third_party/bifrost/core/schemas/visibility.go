// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type VisibilityFilter struct {
	UserIDs         *[]string
	TeamIDs         *[]string
	OwnTeamIDs      *[]string
	VirtualKeyIDs   *[]string
	RoutingScopes   *[]RoutingScopeMatch
	RoleIDs         *[]uint
	BusinessUnitIDs *[]string
	CustomerIDs     *[]string
}
type RoutingScopeMatch struct {
	Scope   string
	ScopeID string
}

func (f *VisibilityFilter) IsUnrestricted() bool {
	if f == nil {
		return true
	}
	return f.UserIDs == nil &&
		f.TeamIDs == nil &&
		f.OwnTeamIDs == nil &&
		f.VirtualKeyIDs == nil &&
		f.RoutingScopes == nil &&
		f.RoleIDs == nil &&
		f.BusinessUnitIDs == nil &&
		f.CustomerIDs == nil
}
