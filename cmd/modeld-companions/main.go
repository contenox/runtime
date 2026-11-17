// Command modeld-companions generates the literal-source tokenizer companions
// the curated catalog requires and the manifest that embeds them in modeld.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/runtime/modelregistry"
)

func main() {
	if len(os.Args) < 2 {
		fail(errors.New("usage: modeld-companions index --assets <dir> [--check] | modeld-companions require --model <name>"))
	}
	switch os.Args[1] {
	case "index":
		runIndex(os.Args[2:])
	case "require":
		runRequire(os.Args[2:])
	default:
		fail(fmt.Errorf("unknown command %q; use index or require", os.Args[1]))
	}
}

func curatedDescriptors() []modelregistry.ModelDescriptor {
	entries, err := modelregistry.NewCurated().List(context.Background())
	if err != nil {
		fail(fmt.Errorf("list the curated catalog: %w", err))
	}
	return entries
}

func runIndex(args []string) {
	flags := flag.NewFlagSet("index", flag.ExitOnError)
	assets := flags.String("assets", "", "directory holding the companion archives and index.json")
	check := flags.Bool("check", false, "verify the published index instead of writing it")
	if err := flags.Parse(args); err != nil {
		fail(err)
	}
	if *assets == "" {
		fail(errors.New("index requires --assets"))
	}
	read := func(id string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(*assets, id+".tar.gz"))
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	index, err := modelstore.CompanionIndex(curatedDescriptors(), read)
	if err != nil {
		fail(err)
	}
	rendered, err := modelstore.RenderCompanionIndex(index)
	if err != nil {
		fail(err)
	}
	path := filepath.Join(*assets, modelstore.CompanionIndexFilename)
	if !*check {
		if err := os.WriteFile(path, rendered, 0o644); err != nil {
			fail(err)
		}
		fmt.Println(path)
		return
	}
	published, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	if !bytes.Equal(published, rendered) {
		fail(fmt.Errorf("%s is stale; regenerate it with: task -d runtime modeld:companions", path))
	}
}

func runRequire(args []string) {
	flags := flag.NewFlagSet("require", flag.ExitOnError)
	name := flags.String("model", "", "curated model name")
	if err := flags.Parse(args); err != nil {
		fail(err)
	}
	if *name == "" {
		fail(errors.New("require needs --model"))
	}
	requirements, err := modelstore.CompanionRequirements(curatedDescriptors())
	if err != nil {
		fail(err)
	}
	for _, requirement := range requirements {
		if requirement.Model.Name != *name {
			continue
		}
		encoded, err := json.MarshalIndent(struct {
			AssetID       string   `json:"assetId"`
			Model         string   `json:"model"`
			ModelRepo     string   `json:"modelRepo"`
			ModelRevision string   `json:"modelRevision"`
			Kind          string   `json:"kind"`
			Format        string   `json:"format"`
			Version       int      `json:"version"`
			Controls      []string `json:"controls"`
		}{requirement.AssetID, requirement.Model.Name, requirement.Model.Repo, requirement.Model.Revision,
			requirement.Kind, requirement.Format, requirement.Version, requirement.Controls}, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(encoded))
		return
	}
	fail(fmt.Errorf("model %q declares no tokenizer companion requirement", *name))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
