package serverops

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

const EmbedPoolID = "internal_embed_pool"
const EmbedPoolName = "Embedder"
const TasksPoolID = "internal_tasks_pool"
const TasksPoolName = "Tasks"
const TenantID = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

type Config struct {
	DatabaseURL         string `json:"database_url"`
	Port                string `json:"port"`
	Addr                string `json:"addr"`
	AllowedAPIOrigins   string `json:"allowed_api_origins"`
	AllowedMethods      string `json:"allowed_methods"`
	AllowedHeaders      string `json:"allowed_headers"`
	SigningKey          string `json:"signing_key"`
	EncryptionKey       string `json:"encryption_key"`
	JWTSecret           string `json:"jwt_secret"`
	JWTExpiry           string `json:"jwt_expiry"`
	TiKVPDEndpoint      string `json:"tikv_pd_endpoint"`
	NATSURL             string `json:"nats_url"`
	NATSUser            string `json:"nats_user"`
	NATSPassword        string `json:"nats_password"`
	SecurityEnabled     string `json:"security_enabled"`
	OpensearchURL       string `json:"opensearch_url"`
	ProxyOrigin         string `json:"proxy_origin"`
	UIBaseURL           string `json:"ui_base_url"`
	TokenizerServiceURL string `json:"tokenizer_service_url"`
	EmbedModel          string `json:"embed_model"`
	TasksModel          string `json:"tasks_model"`
	VectorStoreURL      string `json:"vector_store_url"`
	WorkerUserAccountID string `json:"worker_user_account_id"`
	WorkerUserPassword  string `json:"worker_user_password"`
	WorkerUserEmail     string `json:"worker_user_email"`
}

type ConfigTokenizerService struct {
	Addr                 string `json:"addr"`
	FallbackModel        string `json:"fallback_model"`
	ModelSourceAuthToken string `json:"model_source_auth_token"`
	PreloadModels        string `json:"preload_models"`
	UseDefaultURLs       string `json:"use_default_urls"`
}

func LoadConfig[T any](cfg *T) error {
	config := map[string]string{}
	for _, kvPair := range os.Environ() {
		ar := strings.SplitN(kvPair, "=", 2)
		if len(ar) < 2 {
			continue
		}
		key := strings.ToLower(ar[0])
		value := ar[1]
		config[key] = value
	}

	b, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal env vars: %w", err)
	}
	err = json.Unmarshal(b, cfg)
	if err != nil {
		return fmt.Errorf("failed to unmarshal into config struct: %w", err)
	}

	return nil
}

func ValidateConfig(cfg *Config) error {
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("missing required configuration: database_url")
	}
	if cfg.Port == "" {
		return fmt.Errorf("missing required configuration: port")
	}
	if len(cfg.Addr) == 0 {
		cfg.Addr = "0.0.0.0" // Default to all interfaces
	}
	if len(cfg.AllowedMethods) == 0 {
		cfg.AllowedMethods = "GET, POST, PUT, DELETE, OPTIONS"
		log.Println("allowed_methods not set, using default:", cfg.AllowedMethods)
	}
	if len(cfg.AllowedHeaders) == 0 {
		cfg.AllowedHeaders = "Content-Type, Authorization"
		log.Println("allowed_headers not set, using default:", cfg.AllowedHeaders)
	}
	if len(cfg.AllowedAPIOrigins) == 0 {
		cfg.AllowedAPIOrigins = "*" // Default to allow all origins
		log.Println("allowed_origins not set, using default:", cfg.AllowedAPIOrigins)
	}
	// Validate SigningKey: require at least 16 characters.
	if len(cfg.SigningKey) < 16 {
		return fmt.Errorf("missing or invalid required configuration: signing_key (must be at least 16 characters)")
	}
	// Validate EncryptionKey: require at least 16 characters.
	if len(cfg.EncryptionKey) < 16 {
		return fmt.Errorf("missing or invalid required configuration: encryption_key (must be at least 16 characters)")
	}
	// Validate JWTSecret: require at least 16 characters.
	if len(cfg.JWTSecret) < 16 {
		return fmt.Errorf("missing or invalid required configuration: jwt_secret (must be at least 16 characters)")
	}
	// Ensure UIBaseURL is provided for the reverse proxy.
	if cfg.UIBaseURL == "" {
		return fmt.Errorf("missing required configuration: ui_base_url")
	}
	// Validate SecurityEnabled: must be "true" or "false" (case-insensitive).
	secEnabled := strings.ToLower(strings.TrimSpace(cfg.SecurityEnabled))
	if secEnabled != "true" && secEnabled != "false" {
		return fmt.Errorf("invalid configuration: security_enabled must be 'true' or 'false'")
	}

	if cfg.WorkerUserEmail == "" {
		return fmt.Errorf("missing required configuration: worker_user_email")
	}
	if cfg.WorkerUserPassword == "" {
		return fmt.Errorf("missing required configuration: worker_user_password")
	}
	if cfg.WorkerUserAccountID == "" {
		return fmt.Errorf("missing required configuration: worker_user_account_id")
	}

	if cfg.VectorStoreURL == "" {
		return fmt.Errorf("missing required configuration: vector_store_url")
	}

	if cfg.TasksModel == "" {
		return fmt.Errorf("missing required configuration: tasks_model")
	}

	if cfg.EmbedModel == "" {
		return fmt.Errorf("missing required configuration: embed_model")
	}

	return nil
}
