package libacp

func NegotiateProtocolVersion(theirs, ours int) int {
	if theirs >= 1 && theirs <= ours {
		return theirs
	}
	return ours
}
