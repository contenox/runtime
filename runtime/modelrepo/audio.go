package modelrepo

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrAudioNotSupported    = errors.New("model cannot accept audio input")
	ErrUnsupportedAudioMime = errors.New("unsupported audio media type")
	ErrAudioTooLarge        = errors.New("inline audio exceeds the request size limit")
)

var SupportedAudioMimeTypes = map[string]bool{
	"audio/wav":  true,
	"audio/mpeg": true,
	"audio/mp4":  true,
	"audio/ogg":  true,
	"audio/flac": true,
}

const MaxInlineAudioBytes = 14 << 20

func SupportedAudioMimeList() string {
	types := make([]string, 0, len(SupportedAudioMimeTypes))
	for t := range SupportedAudioMimeTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
}

func MessagesAudioBytes(messages []Message) (parts int, totalBytes int) {
	return countAudioParts(messages)
}

const WAVMimeType = "audio/wav"

func IsWAV(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
}

func AudioMimeFromBytes(data []byte) (mime string, ok bool) {
	mime = sniffAudioMime(data)
	if mime == "" || !SupportedAudioMimeTypes[mime] {
		return "", false
	}
	return mime, true
}

func sniffAudioMime(data []byte) string {
	switch {
	case IsWAV(data):
		return "audio/wav"
	case bytes.HasPrefix(data, []byte("OggS")):
		return "audio/ogg"
	case bytes.HasPrefix(data, []byte("fLaC")):
		return "audio/flac"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return "audio/mp4"
	case bytes.HasPrefix(data, []byte("ID3")), isMPEGFrame(data):
		return "audio/mpeg"
	}
	return ""
}

func isMPEGFrame(data []byte) bool {
	if len(data) < 4 || data[0] != 0xFF || data[1]&0xE0 != 0xE0 {
		return false
	}
	version, layer := (data[1]>>3)&0x03, (data[1]>>1)&0x03
	bitrate, rate := data[2]>>4, (data[2]>>2)&0x03
	return version != 0x01 && layer != 0x00 && bitrate != 0x0F && rate != 0x03
}

func ValidateAudioBytes(providerType, modelName string, messages []Message) error {
	for _, m := range messages {
		for _, a := range m.Audio {
			if _, ok := AudioMimeFromBytes(a.Data); !ok {
				return fmt.Errorf("%w: an audio attachment (declared %q, %d bytes) starts as none of the supported formats, and provider %s (model %s) identifies audio from its bytes; accepted types: %s",
					ErrUnsupportedAudioMime, a.MimeType, len(a.Data), providerType, modelName, SupportedAudioMimeList())
			}
		}
	}
	return nil
}

func countAudioParts(messages []Message) (parts int, totalBytes int) {
	for _, m := range messages {
		for _, a := range m.Audio {
			parts++
			totalBytes += len(a.Data)
		}
	}
	return parts, totalBytes
}

func RefuseAudioInput(providerType, modelName string, messages []Message) error {
	parts, _ := countAudioParts(messages)
	if parts == 0 {
		return nil
	}
	return fmt.Errorf("%w: provider %s (model %s) received %d audio attachment(s) but has no audio input support; use an audio-capable model or remove the audio attachments",
		ErrAudioNotSupported, providerType, modelName, parts)
}

func ValidateAudioParts(providerType, modelName string, messages []Message) error {
	parts, totalBytes := countAudioParts(messages)
	if parts == 0 {
		return nil
	}
	for _, m := range messages {
		for _, a := range m.Audio {
			if !SupportedAudioMimeTypes[a.MimeType] {
				return fmt.Errorf("%w: %q is not accepted by provider %s (model %s); accepted types: %s",
					ErrUnsupportedAudioMime, a.MimeType, providerType, modelName, SupportedAudioMimeList())
			}
		}
	}
	if totalBytes > MaxInlineAudioBytes {
		return fmt.Errorf("%w: request carries %d bytes of audio across %d attachment(s) but provider %s (model %s) accepts at most %d bytes inline; send smaller or fewer audio files",
			ErrAudioTooLarge, totalBytes, parts, providerType, modelName, MaxInlineAudioBytes)
	}
	return nil
}
