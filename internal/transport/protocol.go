package transport

// ProtocolVersion is the modeld wire, session, and worker-control contract this
// build speaks. Bump it for breaking changes to transport requests, responses,
// session behavior, or required process controls. MinProtocol is the oldest
// daemon protocol this build still accepts.
const ProtocolVersion = 4

// MinProtocol requires owner-scoped device claims before native allocation.
const MinProtocol = 4

// Supported reports whether a daemon speaking protocol p is usable by this
// runtime build.
func Supported(p int) bool {
	return p >= MinProtocol && p <= ProtocolVersion
}
