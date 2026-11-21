//go:build media_system

package localtools

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/mediageneration"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/taskengine"
)

var mediaOwner = flag.String("media-owner", "", "running model owner's data directory")
var mediaWorkspace = flag.String("media-workspace", "", "workspace for generated qualification files")
var mediaDevice = flag.String("media-device", "", "optional cpu or physical accelerator ID")
var imageModel = flag.String("media-image-model", "", "selected installed image model")
var videoModel = flag.String("media-video-model", "", "selected installed video model")

// Run against the same installed owner and workspace tool dispatch used by agents.
func TestNativeMediaSystem(t *testing.T) {
	if *mediaOwner == "" || *mediaWorkspace == "" {
		t.Skip("supply -media-owner and -media-workspace")
	}
	if err := os.MkdirAll(*mediaWorkspace, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := modeldconn.WithDataRoot(t.Context(), *mediaOwner)
	models, err := mediageneration.Installed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		t.Logf("installed %s (%s): %s", m.Name, m.Pipeline, m.MediaError)
	}
	for _, kind := range []string{"image", "video"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
			defer cancel()
			if *mediaDevice != "" {
				target := transport.ExecutionTarget{Mode: transport.PlacementCPU}
				if *mediaDevice != "cpu" {
					target = transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: *mediaDevice}
				}
				role := transport.ImageRole
				if kind == "video" {
					role = transport.VideoRole
				}
				var err error
				ctx, err = modeldconn.WithWorkerOptions(ctx, role, modeldconn.WorkerOptions{Target: target})
				if err != nil {
					t.Fatal(err)
				}
			}
			model := *imageModel
			if kind == "video" {
				model = *videoModel
			}
			options := mediageneration.Options{Model: model, Video: kind == "video", Prompt: "A ceramic cup", Seed: 42}
			sample, err := mediageneration.Verify(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			if sample.Execution == nil || sample.Execution.Digest == "" || sample.Execution.ModelName != model {
				t.Fatal("verification did not return the selected model and digest")
			}
			options.Model = sample.Execution.ModelName
			options.Digest = "tree-sha256:0000000000000000000000000000000000000000000000000000000000000000"
			if _, err := mediageneration.Render(ctx, options); !errors.Is(err, transport.ErrDigestMismatch) {
				t.Fatalf("render with a stale digest: %v", err)
			}
			repo := NewLocalFSToolsWith(*mediaWorkspace, nil, nil, LocalFSToolsName, nil, WithMediaRenderer(func(ctx context.Context, opts mediageneration.Options) (transport.MediaResult, error) {
				opts.Model, opts.Digest = sample.Execution.ModelName, sample.Execution.Digest
				return mediageneration.Render(ctx, opts)
			}))
			prompt := "Anime film still of a woman on a coastal rooftop at dusk, navy bob with a white streak, saffron collar, warm light and clean cel shading."
			if kind == "video" {
				prompt = "A little red sailboat glides across a calm blue lake. The wind fills its sail and the boat leaves ripples in its wake. Steady daylight shot."
			}
			args := map[string]any{"prompt": prompt, "seed": 8043, "width": 320, "height": 192}
			if kind == "video" {
				args["width"], args["height"], args["fps"], args["seconds"] = 256, 160, 7, 5
			}
			result, _, err := repo.Exec(ctx, time.Now(), args, false, &taskengine.ToolsCall{ToolName: "generate_" + kind})
			if err != nil {
				t.Fatal(err)
			}
			report, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			t.Log(string(report))
			asset := result.(map[string]any)
			path := filepath.Join(*mediaWorkspace, asset["file"].(string))
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if kind == "image" {
				picture, err := png.Decode(f)
				if err != nil {
					t.Fatal(err)
				}
				if picture.Bounds().Dx() != 320 || picture.Bounds().Dy() != 192 {
					t.Fatalf("image dimensions %v", picture.Bounds())
				}
			} else {
				var header [12]byte
				if _, err := f.ReadAt(header[:], 0); err != nil || string(header[4:8]) != "ftyp" {
					t.Fatalf("invalid MP4 header %x: %v", header, err)
				}
			}
		})
	}
}
