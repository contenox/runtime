package serverops

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL     string `json:"database_url"`
	Port            string `json:"port"`
	Addr            string `json:"addr"`
	AllowedOrigins  string `json:"allowed_origins"`
	AllowedMethods  string `json:"allowed_methods"`
	AllowedHeaders  string `json:"allowed_headers"`
	SigningKey      string `json:"signing_key"`
	EncryptionKey   string `json:"encryption_key"`
	JWTSecret       string `json:"jwt_secret"`
	JWTExpiry       string `json:"jwt_expiry"`
	TiKVPDEndpoint  string `json:"tikv_pd_endpoint"`
	NATSURL         string `json:"nats_url"`
	NATSUser        string `json:"nats_user"`
	NATSPassword    string `json:"nats_password"`
	SecurityEnabled string `json:"security_enabled"`
	OpensearchURL   string `json:"opensearch_url"`
	ProxyOrigin     string `json:"proxy_origin"`
}

func LoadConfig(cfg *Config) error {
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

	// Handle JSON unmarshalling (for other fields)
	b, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("this is a bug, loadConfig failed to marshal environment variables: %w", err)
	}
	err = json.Unmarshal(b, cfg)
	if err != nil {
		return fmt.Errorf("this is a bug, loadConfig failed to unmarshal config: %w", err)
	}

	return nil
}

func ValidateConfig(cfg *Config) error {
	if cfg.DatabaseURL == "" {
		return fmt.Errorf("missing required configuration: DatabaseURL")
	}
	if cfg.Port == "" {
		return fmt.Errorf("missing required configuration: Port")
	}
	if len(cfg.Addr) == 0 {
		cfg.Addr = "0.0.0.0" // Default to all interfaces
	}
	if len(cfg.AllowedMethods) == 0 {
		cfg.AllowedMethods = "GET, POST, PUT, DELETE, OPTIONS"
		log.Println("AllowedMethods not set, using default:", cfg.AllowedMethods)
	}
	if len(cfg.AllowedHeaders) == 0 {
		cfg.AllowedHeaders = "Content-Type, Authorization"
		log.Println("AllowedHeaders not set, using default:", cfg.AllowedHeaders)
	}
	if len(cfg.AllowedOrigins) == 0 {
		cfg.AllowedOrigins = "*" // Default to allow all origins
		log.Println("AllowedOrigins not set, using default:", cfg.AllowedOrigins)
	}

	// Validate SigningKey
	if len(cfg.SigningKey) < 16 {
		return fmt.Errorf("missing or invalid required configuration: SigningKey (must be at least 8 characters)")
	}

	// Validate EncryptionKey
	if len(cfg.EncryptionKey) < 16 {
		return fmt.Errorf("missing or invalid required configuration: EncryptionKey (must be at least 8 characters)")
	}

	// Validate JWTSecret
	if len(cfg.JWTSecret) < 16 {
		return fmt.Errorf("missing or invalid required configuration: JWTSecret (must be at least 16 characters)")
	}
	_, err := strconv.Atoi(cfg.JWTExpiry)
	if err != nil {
		return fmt.Errorf("invalid JWTExpiry format: must be a number representing hours (e.g., '24' for 24 hours) %w", err)
	}

	if !strings.Contains("true false", cfg.SecurityEnabled) {
		return fmt.Errorf("invalig configuration security is not set to true or false")
	}

	return nil
}
