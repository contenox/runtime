package modelrepo

import "strings"

var (
	openAINonVisionMarkers = []string{
		"embedding", "tts", "whisper", "transcribe", "-audio", "realtime",
		"dall-e", "sora", "gpt-image", "moderation", "-search",
	}
	openAITextOnlyPrefixes = []string{
		"gpt-3.5", "gpt-4-turbo-preview", "o1-mini", "o1-preview", "o3-mini",
	}
	openAIVisionPrefixes = []string{
		"gpt-5", "gpt-4o", "gpt-4.1", "gpt-4-turbo", "chatgpt-4o",
		"o1", "o3", "o4", "computer-use",
	}
)

func OpenAIModelSupportsVision(modelName string) bool {
	n := strings.ToLower(strings.TrimSpace(modelName))
	for _, m := range openAINonVisionMarkers {
		if strings.Contains(n, m) {
			return false
		}
	}
	if strings.Contains(n, "vision-preview") {
		return true
	}
	for _, p := range openAITextOnlyPrefixes {
		if strings.HasPrefix(n, p) {
			return false
		}
	}
	for _, p := range openAIVisionPrefixes {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}
