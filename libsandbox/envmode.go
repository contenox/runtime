package libsandbox

const (
	ScrubOff         = "off"
	ScrubDenySecrets = "deny-secrets"
	ScrubStrict      = "strict"
)

func EnvPolicyForMode(mode string, extraAllow, extraDeny []string) (policy EnvPolicy, active bool) {
	switch mode {
	case ScrubStrict:
		return EnvPolicy{
			Allow: concat(DefaultEnvAllow(), extraAllow),
			Deny:  concat(ControlPlaneEnvDeny(), extraDeny),
		}, true
	case ScrubDenySecrets:
		return EnvPolicy{
			Allow: concat([]string{"*"}, extraAllow),
			Deny:  concat(DefaultEnvDeny(), extraDeny),
		}, true
	default:
		return EnvPolicy{}, false
	}
}

func EnvScrub(mode string, extraAllow, extraDeny []string) func(parent []string) []string {
	policy, active := EnvPolicyForMode(mode, extraAllow, extraDeny)
	if !active {
		return nil
	}
	return policy.Apply
}

func ScrubModeValid(mode string) bool {
	switch mode {
	case ScrubOff, ScrubDenySecrets, ScrubStrict:
		return true
	default:
		return false
	}
}
