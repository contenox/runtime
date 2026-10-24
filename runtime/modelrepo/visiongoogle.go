package modelrepo

import "strings"

var geminiVisionFamilies = []string{
	"gemini-1.5",
	"gemini-2.0",
	"gemini-2.5",
	"gemini-3",
	"gemini-pro-vision",
}

var geminiNonVisionMarkers = []string{
	"embedding",
	"tts",
	"live-translate",
	"lyria",
	"veo",
	"imagen",
	"aqa",
}

func GeminiModelSupportsVision(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	for _, marker := range geminiNonVisionMarkers {
		if strings.Contains(name, marker) {
			return false
		}
	}
	for _, family := range geminiVisionFamilies {
		if strings.HasPrefix(name, family) {
			return true
		}
	}
	return false
}
