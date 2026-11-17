package inferencesetup

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
)

// InputRequirement selects the input capabilities a Chat or Recovery route must verify.
type InputRequirement string

const (
	// InputText requires text and structured tool use.
	InputText InputRequirement = "text"
	// InputImages also requires image understanding.
	InputImages InputRequirement = "images"
	// InputAudio also requires audio understanding.
	InputAudio InputRequirement = "audio"
	// InputImagesAndAudio requires both image and audio understanding.
	InputImagesAndAudio InputRequirement = "images-and-audio"
)

func (r InputRequirement) normalized() InputRequirement {
	if r == "" {
		return InputText
	}
	return r
}

func (r InputRequirement) validate(role string) error {
	r = r.normalized()
	if r != InputText && role != RoleChat && role != RoleRecovery {
		return errors.New("image and audio input requirements apply to Chat and Recovery")
	}
	switch r {
	case InputText, InputImages, InputAudio, InputImagesAndAudio:
		return nil
	default:
		return fmt.Errorf("unknown input requirement %q", r)
	}
}

func (r InputRequirement) images() bool { return r == InputImages || r == InputImagesAndAudio }
func (r InputRequirement) audio() bool  { return r == InputAudio || r == InputImagesAndAudio }

func verifyInputs(ctx context.Context, requirement InputRequirement, chat ChatOperation) error {
	if requirement.images() {
		for _, probe := range []struct {
			name  string
			color color.RGBA
		}{{"red", color.RGBA{R: 255, A: 255}}, {"blue", color.RGBA{B: 255, A: 255}}} {
			img := image.NewRGBA(image.Rect(0, 0, 224, 224))
			draw.Draw(img, img.Bounds(), image.NewUniform(probe.color), image.Point{}, draw.Src)
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, img); err != nil {
				return err
			}
			message := modelrepo.Message{Role: "user", Content: "What color is this image? Reply with only the English color name.", Images: []modelrepo.ImagePart{{Data: encoded.Bytes(), MimeType: "image/png"}}}
			if err := verifyInputAnswer(ctx, chat, message, probe.name); err != nil {
				return fmt.Errorf("image verification failed; check the model's vision assets and selected backend: %w", err)
			}
		}
	}
	if requirement.audio() {
		for _, count := range []int{2, 3} {
			message := modelrepo.Message{Role: "user", Content: "How many separate beeps are in this recording? Reply with only the number as a digit.", Audio: []modelrepo.AudioPart{{Data: beepWAV(count), MimeType: modelrepo.WAVMimeType}}}
			if err := verifyInputAnswer(ctx, chat, message, fmt.Sprint(count)); err != nil {
				return fmt.Errorf("audio verification failed; select an audio-capable model and adapter: %w", err)
			}
		}
	}
	return nil
}

func verifyInputAnswer(ctx context.Context, chat ChatOperation, message modelrepo.Message, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := chat(ctx, []modelrepo.Message{message}, modelrepo.WithMaxTokens(64), modelrepo.WithThink("off"), modelrepo.WithTemperature(0))
	if err != nil {
		return err
	}
	answer := strings.Trim(strings.ToLower(strings.TrimSpace(result.Message.Content)), ".!\"'")
	if len(result.ToolCalls) != 0 || answer != expected {
		return errors.New("the response did not identify the attached sample")
	}
	return ctx.Err()
}

func beepWAV(count int) []byte {
	const rate = 16000
	const toneSamples = rate / 2
	const gapSamples = rate / 2
	samples := rate + count*(toneSamples+gapSamples)
	data := make([]byte, 44+samples*2)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], rate)
	binary.LittleEndian.PutUint32(data[28:], rate*2)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(samples*2))
	for beep := 0; beep < count; beep++ {
		start := rate/2 + beep*(toneSamples+gapSamples)
		for i := 0; i < toneSamples; i++ {
			envelope := min(1, float64(min(i, toneSamples-1-i))/160)
			value := int16(12000 * envelope * math.Sin(2*math.Pi*440*float64(i)/rate))
			binary.LittleEndian.PutUint16(data[44+(start+i)*2:], uint16(value))
		}
	}
	return data
}
