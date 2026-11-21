// Package mediageneration renders workspace assets through the configured native model owner.
package mediageneration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/google/uuid"
)

const (
	DefaultDimension    = 384
	DefaultVideoFPS     = 8
	DefaultVideoSeconds = 2
)

type OutputOptions struct {
	Width   *int     `json:"width,omitempty"`
	Height  *int     `json:"height,omitempty"`
	FPS     *int     `json:"fps,omitempty"`
	Seconds *float64 `json:"seconds,omitempty"`
}

type Options struct {
	OutputOptions
	Model  string
	Digest string
	Prompt string
	Video  bool
	Seed   uint32
}

func Installed(ctx context.Context) ([]modelstore.NodeModel, error) {
	inventory, err := modelstore.NewAdmin(modelstore.Dir(modeldconn.DataRootFromContext(ctx), "")).Inventory()
	if err != nil {
		return nil, err
	}
	var models []modelstore.NodeModel
	for _, model := range inventory {
		if model.Pipeline != "" {
			models = append(models, model)
		}
	}
	return models, nil
}

func Render(ctx context.Context, opts Options) (transport.MediaResult, error) {
	return render(ctx, opts, DefaultDimension, 24)
}

// Verify exercises the same tokenizer, denoiser, decoder and encoder with a small sample.
func Verify(ctx context.Context, opts Options) (transport.MediaResult, error) {
	return render(ctx, opts, 256, 2)
}

func (opts Options) settings(side, steps int) (transport.MediaSettings, error) {
	s := transport.MediaSettings{Prompt: opts.Prompt, Width: side, Height: side, Frames: 1, Steps: steps, Seed: opts.Seed}
	if opts.Width != nil {
		s.Width = *opts.Width
	}
	if opts.Height != nil {
		s.Height = *opts.Height
	}
	if opts.Video {
		s.FPS = DefaultVideoFPS
		if opts.FPS != nil {
			s.FPS = *opts.FPS
		}
		seconds := float64(DefaultVideoSeconds)
		if opts.Seconds != nil {
			seconds = *opts.Seconds
		}
		if s.FPS < 1 || s.FPS > transport.MaxMediaFPS {
			return s, fmt.Errorf("fps must be between 1 and %d", transport.MaxMediaFPS)
		}
		frames := seconds * float64(s.FPS)
		rounded := math.Round(frames)
		if !(rounded >= 1 && rounded <= transport.MaxMediaPixels) || math.Abs(frames-rounded) > 1e-9 {
			return s, fmt.Errorf("duration %g seconds at %d fps must produce a whole number of frames within the render budget", seconds, s.FPS)
		}
		s.Frames = int(rounded) + 1
	} else if opts.Seconds != nil || opts.FPS != nil {
		return s, errors.New("seconds and fps apply only to video")
	}
	return s, s.Validate()
}

func render(ctx context.Context, opts Options, side, steps int) (transport.MediaResult, error) {
	settings, err := opts.settings(side, steps)
	if err != nil {
		return transport.MediaResult{}, err
	}
	if opts.Model == "" {
		return transport.MediaResult{}, fmt.Errorf("select a model for %s in Local AI", settings.Role())
	}
	inventory, err := Installed(ctx)
	if err != nil {
		return transport.MediaResult{}, err
	}
	for _, item := range inventory {
		if item.Name != opts.Model {
			continue
		}
		if item.MediaError != "" {
			return transport.MediaResult{}, errors.New(item.MediaError)
		}
		if transport.MediaRoleForPipeline(item.Pipeline) != settings.Role() {
			return transport.MediaResult{}, fmt.Errorf("selected model %q does not support %s", item.Name, settings.Role())
		}
		return modeldconn.RenderMediaTarget(ctx, modeldconn.ModeldTarget{}, transport.MediaRequest{
			OpenSessionRequest: transport.OpenSessionRequest{ModelName: item.Name, Type: item.Type, Digest: opts.Digest, Config: transport.Config{Operation: "render-media"}},
			Settings:           settings,
		})
	}
	return transport.MediaResult{}, fmt.Errorf("selected %s model %q is not installed", settings.Role(), opts.Model)
}

// WriteNew publishes a completed render inside a workspace without replacing an existing file.
func WriteNew(ctx context.Context, workspace, relative string, data []byte) error {
	if !filepath.IsLocal(relative) {
		return errors.New("choose a workspace-relative output path")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	parent, leaf, err := vfs.OpenFileParent(root, filepath.Clean(relative), true)
	if err != nil {
		return err
	}
	defer parent.Close()
	temporary := "." + uuid.NewString() + ".part"
	file, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer parent.Remove(temporary)
	_, err = file.Write(data)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return parent.Link(temporary, leaf)
}
