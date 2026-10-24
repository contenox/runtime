package modelrepo

type chatArgument struct {
	applyFunc func(*ChatConfig)
}

func (c *chatArgument) Apply(config *ChatConfig) {
	c.applyFunc(config)
}

func WithTemperature(temp float64) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.Temperature = &temp
		},
	}
}

func WithMaxTokens(tokens int) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.MaxTokens = &tokens
		},
	}
}

func WithTopP(p float64) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.TopP = &p
		},
	}
}

func WithSeed(seed int) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.Seed = &seed
		},
	}
}

func WithTool(tool Tool) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.Tools = append(config.Tools, tool)
		},
	}
}

func WithTools(tools ...Tool) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.Tools = append(config.Tools, tools...)
		},
	}
}

func WithCacheHints(hints CacheHints) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			h := hints
			config.CacheHints = &h
		},
	}
}

func WithStopSequences(sequences ...string) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.StopSequences = append([]string(nil), sequences...)
		},
	}
}

func WithToolChoice(choice ToolChoice) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			c := choice
			config.ToolChoice = &c
		},
	}
}

func WithParallelToolCalls(enabled bool) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			config.ParallelToolCalls = &enabled
		},
	}
}

func WithResponseFormat(format ResponseFormat) ChatArgument {
	return &chatArgument{
		applyFunc: func(config *ChatConfig) {
			f := format
			config.Format = &f
		},
	}
}
