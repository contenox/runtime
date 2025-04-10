package serverops

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/js402/CATE/libs/libauth"
)

var ServiceManagerInstance atomic.Pointer[ServiceManager]

var _ ServiceManager = &serviceManager{}

type ServiceManager interface {
	RegisterServices(s ...ServiceMeta) error
	GetServices() ([]ServiceMeta, error)
	IsSecurityEnabled(serviceName string) bool
	HasValidLicenseFor(serviceName string) bool
	GetSecret() string
	GetTokenExpiry() time.Duration
}

type ServiceMeta interface {
	GetServiceName() string
	GetServiceGroup() string
}

// serviceManager is a thread-safe in-memory storage for services.
type serviceManager struct {
	mu       sync.RWMutex
	services []ServiceMeta
	config   *Config
	expriry  time.Duration
}

// NewServiceManager creates a new instance of server.
func NewServiceManager(config *Config) error {
	expriry, err := time.ParseDuration(config.JWTExpiry)
	if err != nil {
		return err
	}
	var s ServiceManager = &serviceManager{
		services: make([]ServiceMeta, 0),
		config:   config,
		mu:       sync.RWMutex{},
		expriry:  expriry,
	}
	ServiceManagerInstance.Store(&s)

	return nil
}

// RegisterService adds a service to the repository.
func (r *serviceManager) RegisterServices(s ...ServiceMeta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services = append(r.services, s...)
	return nil
}

// GetServices returns a list of registered services.
func (r *serviceManager) GetServices() ([]ServiceMeta, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	copiedServices := make([]ServiceMeta, len(r.services))
	copy(copiedServices, r.services)
	return copiedServices, nil
}

// HasValidLicenseFor checks if a service is allowed to run based on the license.
func (r *serviceManager) HasValidLicenseFor(serviceName string) bool {
	return true // TODO: Implement license validation
}

// IsSecurityEnabled checks if security is enabled.
func (r *serviceManager) IsSecurityEnabled(serviceName string) bool {
	if r.config == nil {
		return false
	}
	return r.config.SecurityEnabled == "true" || r.config.SecurityEnabled == "1"
}

// GetSecret implements ServiceManager.
func (r *serviceManager) GetSecret() string {
	return r.config.JWTSecret
}

func (r *serviceManager) GetTokenExpiry() time.Duration {
	return r.expriry
}

func GetManagerInstance() ServiceManager {
	if instance := ServiceManagerInstance.Load(); instance != nil {
		return *instance
	}
	return nil
}

// GetIdentity extracts the identity from the context using the JWT secret from the ServiceManager.
func GetIdentity[T libauth.Authz](ctx context.Context) (string, error) {
	manager := GetManagerInstance()
	if manager == nil {
		return "", fmt.Errorf("service manager is not initialized")
	}

	jwtSecret := manager.GetSecret()
	if jwtSecret == "" {
		return "", libauth.ErrEmptyJWTSecret
	}

	return libauth.GetIdentity[T](ctx, jwtSecret)
}
