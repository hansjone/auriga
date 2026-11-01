package schemas

func (d *BifrostRoutingMetadata) Clone() *BifrostRoutingMetadata {
	if d == nil || len(d.Calls) == 0 {
		return nil
	}
	clone := &BifrostRoutingMetadata{
		Calls: make([]BifrostRoutingCall, len(d.Calls)),
	}
	for index, call := range d.Calls {
		clone.Calls[index] = call
		clone.Calls[index].ProviderUsed = cloneString(call.ProviderUsed)
		clone.Calls[index].ModelUsed = cloneString(call.ModelUsed)
		clone.Calls[index].InputTokens = cloneInt(call.InputTokens)
		clone.Calls[index].OutputTokens = cloneInt(call.OutputTokens)
	}
	return clone
}
