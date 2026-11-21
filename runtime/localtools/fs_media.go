package localtools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/mediageneration"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/google/uuid"
)

type MediaRenderer func(context.Context, mediageneration.Options) (transport.MediaResult, error)

func WithMediaRenderer(render MediaRenderer) FSOption {
	return func(h *LocalFSTools) { h.renderMedia = render }
}

func (h *LocalFSTools) generateMedia(ctx context.Context, args map[string]any, video bool) (any, taskengine.DataType, error) {
	if h.renderMedia == nil {
		return nil, taskengine.DataTypeAny, errors.New("media roles are unavailable in this runtime")
	}
	if !h.fileIOIsOS {
		return nil, taskengine.DataTypeAny, errors.New("media generation requires a local workspace")
	}
	parameters := []string{"path", "prompt", "seed", "width", "height"}
	method := "generate_image"
	if video {
		method = "generate_video"
		parameters = append(parameters, "fps", "seconds")
	}
	if err := rejectUnknownArgs("local_fs."+method, args, parameters...); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	prompt, _ := argString(args, "prompt")
	opts := mediageneration.Options{Prompt: prompt, Video: video, Seed: 42}
	var err error
	if opts.Width, err = mediaInteger(args, "width"); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if opts.Height, err = mediaInteger(args, "height"); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if opts.FPS, err = mediaInteger(args, "fps"); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if _, ok := args["seconds"]; ok {
		secs, ok := argFloat(args, "seconds")
		if !ok || !(secs > 0) || math.IsInf(secs, 0) {
			return nil, taskengine.DataTypeAny, errors.New("seconds must be a positive number")
		}
		opts.Seconds = &secs
	}
	if _, ok := args["seed"]; ok {
		seed, ok := argFloat(args, "seed")
		if !ok || seed < 0 || seed > math.MaxUint32 || math.Trunc(seed) != seed {
			return nil, taskengine.DataTypeAny, errors.New("seed must be an unsigned 32-bit integer")
		}
		opts.Seed = uint32(seed)
	}
	path, err := mediaPath(args, video)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	abs, err := h.checkPath(ctx, path)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if err := h.checkDeniedSubstrings(ctx, abs); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if _, err := os.Lstat(abs); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = fmt.Errorf("%s already exists; choose a new output file", path)
		}
		return nil, taskengine.DataTypeAny, err
	}
	base, err := h.absAllowedDir(ctx)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	relative, err := filepath.Rel(base, abs)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if filepath.ToSlash(relative) != path {
		return nil, taskengine.DataTypeAny, fmt.Errorf("use the resolved output path %q", filepath.ToSlash(relative))
	}
	result, err := h.renderMedia(ctx, opts)
	if err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	if err := mediageneration.WriteNew(ctx, base, relative, result.Data); err != nil {
		return nil, taskengine.DataTypeAny, err
	}
	return map[string]any{"file": filepath.ToSlash(relative), "mime": result.MIME, "execution": result.Execution}, taskengine.DataTypeAny, nil
}

func mediaInteger(args map[string]any, key string) (*int, error) {
	if _, exists := args[key]; !exists {
		return nil, nil
	}
	n, ok := argFloat(args, key)
	if !ok || n < 1 || n > math.MaxInt32 || math.Trunc(n) != n {
		return nil, fmt.Errorf("%s must be a positive integer", key)
	}
	value := int(n)
	return &value, nil
}

func mediaPath(args map[string]any, video bool) (string, error) {
	extension := ".png"
	if video {
		extension = ".mp4"
	}
	path, ok := args["path"].(string)
	if !ok && args["path"] != nil {
		return "", errors.New("media output path must be a string")
	}
	if path == "" {
		return "Media/" + uuid.NewString() + extension, nil
	}
	if !filepath.IsLocal(path) || !strings.EqualFold(filepath.Ext(path), extension) {
		return "", fmt.Errorf("choose a new workspace %s file", extension)
	}
	return filepath.ToSlash(filepath.Clean(path)), nil
}
