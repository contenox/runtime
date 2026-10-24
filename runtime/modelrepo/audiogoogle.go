package modelrepo

import "strings"

var geminiAudioFamilies = []string{
	"gemini-1.5",
	"gemini-2.0",
	"gemini-2.5",
	"gemini-3",
}

var geminiNonAudioMarkers = []string{
	"embedding",
	"tts",
	"live-translate",
	"lyria",
	"veo",
	"imagen",
	"aqa",
}

func GeminiModelSupportsAudio(modelName string) bool {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	for _, marker := range geminiNonAudioMarkers {
		if strings.Contains(name, marker) {
			return false
		}
	}
	for _, family := range geminiAudioFamilies {
		if strings.HasPrefix(name, family) {
			return true
		}
	}
	return false
}
