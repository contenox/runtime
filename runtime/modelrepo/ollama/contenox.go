package ollama

const (
	ExtensionAudios          = "audios"
	ExtensionSession         = "session"
	ExtensionFillInTheMiddle = "fill-in-the-middle"
	ExtensionNextEdit        = "next-edit"
	ExtensionEmbeddings      = "embeddings"
)

const ProductContenoxGateway = "contenox-gateway"

const ContenoxPath = "/api/contenox"

const contenoxRoute = "/contenox"

type ContenoxResponse struct {
	Product       string   `json:"product"`
	Version       string   `json:"version"`
	Build         string   `json:"build,omitempty"`
	OllamaVersion string   `json:"ollama_version,omitempty"`
	Extensions    []string `json:"extensions,omitempty"`
}

func (r ContenoxResponse) IsContenoxGateway() bool {
	return r.Product == ProductContenoxGateway
}

func (r ContenoxResponse) Supports(extension string) bool {
	for _, declared := range r.Extensions {
		if declared == extension {
			return true
		}
	}
	return false
}
