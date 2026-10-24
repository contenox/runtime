package modelrepo

func IsLocalBackendType(backendType string) bool {
	switch CanonicalBackendType(backendType) {
	case "llama", "openvino", "modeld", "local":
		return true
	default:
		return false
	}
}
