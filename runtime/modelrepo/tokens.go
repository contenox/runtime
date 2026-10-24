package modelrepo

func ClampMaxOutputTokens(requested, ceiling int) (int, bool) {
	if requested > 0 && ceiling > 0 && requested > ceiling {
		return ceiling, true
	}
	return requested, false
}

func ClampMaxOutputTokensPtr(tokens *int, ceiling int) *int {
	if tokens == nil {
		return nil
	}
	v, _ := ClampMaxOutputTokens(*tokens, ceiling)
	return &v
}
