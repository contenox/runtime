package diffusion

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errDynamicTruncation = errors.New("this tokenizer uses a dynamic truncation length; install a model export compatible with OpenVINO GenAI 2026.2")

// CheckCompatibility checks the graph contracts used by the pinned native pipeline.
func CheckCompatibility(dir, pipeline string) error {
	switch pipeline {
	case "LTXPipeline":
		_, err := NeedsLTXAdapter(dir)
		return err
	case "":
		return nil
	default:
		return checkTokenizer(dir)
	}
}

// NeedsLTXAdapter selects the component-level adapter only for graph signatures
// that GenAI 2026.2's Text2VideoPipeline cannot consume directly.
func NeedsLTXAdapter(dir string) (bool, error) {
	rank, err := videoTimestepRank(filepath.Join(dir, "transformer", "openvino_model.xml"))
	if err != nil || rank == 2 {
		return rank == 2, err
	}
	err = checkTokenizer(dir)
	if errors.Is(err, errDynamicTruncation) {
		return true, nil
	}
	return false, err
}

func checkTokenizer(dir string) error {
	f, err := os.Open(filepath.Join(dir, "tokenizer", "openvino_tokenizer.xml"))
	if err != nil {
		return err
	}
	defer f.Close()
	var tokenizer struct {
		XMLName xml.Name `xml:"net"`
		Layers  []struct {
			ID     string `xml:"id,attr"`
			Type   string `xml:"type,attr"`
			Inputs []struct {
				ID string `xml:"id,attr"`
			} `xml:"input>port"`
		} `xml:"layers>layer"`
		Edges []struct {
			From string `xml:"from-layer,attr"`
			To   string `xml:"to-layer,attr"`
			Port string `xml:"to-port,attr"`
		} `xml:"edges>edge"`
	}
	if err := xml.NewDecoder(f).Decode(&tokenizer); err != nil {
		return fmt.Errorf("read OpenVINO tokenizer graph: %w", err)
	}
	types := make(map[string]string, len(tokenizer.Layers))
	type input struct{ layer, port string }
	edges := make(map[input]string, len(tokenizer.Edges))
	for _, e := range tokenizer.Edges {
		edges[input{e.To, e.Port}] = e.From
	}
	for _, layer := range tokenizer.Layers {
		types[layer.ID] = layer.Type
	}
	for _, layer := range tokenizer.Layers {
		if layer.Type != "Truncate" {
			continue
		}
		// Tokenizers place the length before the final two truncation options.
		if len(layer.Inputs) < 3 || types[edges[input{layer.ID, layer.Inputs[len(layer.Inputs)-3].ID}]] != "Const" {
			return errDynamicTruncation
		}
	}
	return nil
}

// Parameter layers precede the denoiser weights. Stop reading as soon as the
// timestep is found so inventory never has to decode the full transformer IR.
func videoTimestepRank(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	decoder := xml.NewDecoder(f)
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return 0, errors.New("LTX transformer is missing its timestep input")
		}
		if err != nil {
			return 0, fmt.Errorf("read LTX transformer graph: %w", err)
		}
		node, ok := token.(xml.StartElement)
		if !ok || node.Name.Local != "layer" {
			continue
		}
		var name, kind string
		for _, attr := range node.Attr {
			switch attr.Name.Local {
			case "name":
				name = attr.Value
			case "type":
				kind = attr.Value
			}
		}
		if kind != "Parameter" || name != "timestep" {
			continue
		}
		var parameter struct {
			Data struct {
				Shape string `xml:"shape,attr"`
			} `xml:"data"`
		}
		if err := decoder.DecodeElement(&parameter, &node); err != nil {
			return 0, err
		}
		rank := strings.Count(parameter.Data.Shape, ",") + 1
		if parameter.Data.Shape == "" || rank > 2 {
			return 0, errors.New("LTX timestep must be a step per batch or per video token")
		}
		return rank, nil
	}
}
