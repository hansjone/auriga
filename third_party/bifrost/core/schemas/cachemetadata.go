// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

func (d *BifrostCacheMetadata) Clone() *BifrostCacheMetadata {
	if d == nil {
		return nil
	}
	clone := *d
	clone.CacheID = cloneString(d.CacheID)
	clone.HitType = cloneString(d.HitType)
	clone.RequestedProvider = cloneString(d.RequestedProvider)
	clone.RequestedModel = cloneString(d.RequestedModel)
	clone.ProviderUsed = cloneString(d.ProviderUsed)
	clone.ModelUsed = cloneString(d.ModelUsed)
	clone.InputTokens = cloneInt(d.InputTokens)
	clone.Threshold = cloneFloat64(d.Threshold)
	clone.Similarity = cloneFloat64(d.Similarity)
	clone.CacheHitLatency = cloneInt64(d.CacheHitLatency)
	return &clone
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
