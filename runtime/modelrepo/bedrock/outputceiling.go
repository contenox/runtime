package bedrock

import (
	"regexp"
	"strconv"
	"sync"
)

var learnedOutputCeilings sync.Map

var outputCeilingPattern = regexp.MustCompile(`maximum tokens you requested exceeds the model limit of (\d+)`)

func (c *bedrockClient) effectiveModel() converseModel {
	m := c.model
	if v, ok := learnedOutputCeilings.Load(m.name); ok {
		if learned := v.(int); m.maxOutputTokens == 0 || learned < m.maxOutputTokens {
			m.maxOutputTokens = learned
		}
	}
	return m
}

func (c *bedrockClient) learnOutputCeiling(err error) bool {
	if err == nil {
		return false
	}
	match := outputCeilingPattern.FindStringSubmatch(err.Error())
	if match == nil {
		return false
	}
	limit, convErr := strconv.Atoi(match[1])
	if convErr != nil || limit <= 0 {
		return false
	}
	if current := c.effectiveModel().maxOutputTokens; current > 0 && current <= limit {
		return false
	}
	learnedOutputCeilings.Store(c.model.name, limit)
	return true
}
