package modelrepo_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

var audioSamples = map[string][]byte{
	"audio/wav":  []byte("RIFF\x24\x00\x00\x00WAVEfmt "),
	"audio/mpeg": {0x49, 0x44, 0x33, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
	"audio/mp4":  []byte("\x00\x00\x00\x20ftypM4A \x00\x00\x00\x00"),
	"audio/ogg":  []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00"),
	"audio/flac": []byte("fLaC\x00\x00\x00\x22"),
}

func TestUnit_AudioMimeFromBytes_NamesEachSupportedType(t *testing.T) {
	t.Parallel()

	for want, data := range audioSamples {
		got, ok := modelrepo.AudioMimeFromBytes(data)
		require.True(t, ok, "%s", want)
		require.Equal(t, want, got)
	}

	frame := []byte{0xFF, 0xFB, 0x90, 0x64}
	got, ok := modelrepo.AudioMimeFromBytes(frame)
	require.True(t, ok, "an MPEG layer III frame with no ID3 tag is audio")
	require.Equal(t, "audio/mpeg", got)
}

func TestUnit_AudioMimeFromBytes_CoversEverySupportedType(t *testing.T) {
	t.Parallel()

	for mime := range modelrepo.SupportedAudioMimeTypes {
		data, ok := audioSamples[mime]
		require.True(t, ok, "%s is accepted but no sample proves it is detectable", mime)
		got, detected := modelrepo.AudioMimeFromBytes(data)
		require.True(t, detected, "%s is accepted but its bytes are not recognised", mime)
		require.Equal(t, mime, got)
	}
}

func TestUnit_AudioMimeFromBytes_RefusesWhatIsNotSupportedAudio(t *testing.T) {
	t.Parallel()

	for name, data := range map[string][]byte{
		"nil":                nil,
		"empty":              {},
		"text":               []byte("hello, this is not audio"),
		"png":                []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
		"riff but not wave":  []byte("RIFF\x24\x00\x00\x00AVI LIST"),
		"truncated riff":     []byte("RIFF\x24\x00\x00\x00WAV"),
		"adts aac":           {0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC},
		"mpeg reserved rate": {0xFF, 0xFB, 0x9C, 0x64},
		"frame sync alone":   {0xFF, 0xE0},
		"ftyp too short":     []byte("\x00\x00\x00\x08ftyp"),
	} {
		mime, ok := modelrepo.AudioMimeFromBytes(data)
		require.False(t, ok, "%s must not read as audio, got %q", name, mime)
		require.Empty(t, mime, name)
	}
}

func TestUnit_ValidateAudioBytes_RefusesAnAttachmentThatIsNotAudio(t *testing.T) {
	t.Parallel()

	good := []modelrepo.Message{{Role: "user", Audio: []modelrepo.AudioPart{{Data: audioSamples["audio/flac"], MimeType: "audio/flac"}}}}
	require.NoError(t, modelrepo.ValidateAudioBytes("ollama", "m", good))
	require.NoError(t, modelrepo.ValidateAudioBytes("ollama", "m", []modelrepo.Message{{Role: "user", Content: "hi"}}))

	bad := []modelrepo.Message{{Role: "user", Audio: []modelrepo.AudioPart{{Data: []byte("not audio"), MimeType: "audio/wav"}}}}
	err := modelrepo.ValidateAudioBytes("ollama", "m", bad)
	require.ErrorIs(t, err, modelrepo.ErrUnsupportedAudioMime)
	require.Contains(t, err.Error(), "audio/flac", "the refusal lists what would have been accepted")
}
