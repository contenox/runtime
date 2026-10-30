package runtimetypes

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/winproc"
	"github.com/stretchr/testify/require"
)

const MAXLIMIT = 1000

var ErrLimitParamExceeded = fmt.Errorf("limit exceeds maximum allowed value")
var ErrAppendLimitExceeded = fmt.Errorf("append limit exceeds maximum allowed values")

type Status struct {
	Status    string `json:"status" example:"downloading"`
	Digest    string `json:"digest,omitempty" example:"sha256:9e3a6c0d3b5e7f8a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a"`
	Total     int64  `json:"total,omitempty" example:"1000000"`
	Completed int64  `json:"completed,omitempty" example:"250000"`
	Model     string `json:"model" example:"mistral:instruct"`
	BaseURL   string `json:"baseUrl" example:"http://ollama-prod.internal:11434"`
}

type QueueItem struct {
	URL   string `json:"url" example:"http://ollama-prod.internal:11434"`
	Model string `json:"model" example:"llama2:latest"`
}

type Backend struct {
	Revision int64  `json:"revision"`
	ID       string `json:"id" example:"b7d9e1a3-8f0c-4a7d-9b1e-2f3a4b5c6d7e"`
	Name     string `json:"name" example:"ollama-production"`
	BaseURL  string `json:"baseUrl" example:"http://ollama-prod.internal:11434"`
	Type     string `json:"type" example:"ollama"`

	CreatedAt time.Time `json:"createdAt" example:"2023-11-15T14:30:45Z"`
	UpdatedAt time.Time `json:"updatedAt" example:"2023-11-15T14:30:45Z"`
}

type Model struct {
	ID            string    `json:"id" example:"m7d8e9f0a-1b2c-3d4e-5f6a-7b8c9d0e1f2a"`
	Model         string    `json:"model" example:"mistral:instruct"`
	ContextLength int       `json:"contextLength" example:"8192"`
	CanChat       bool      `json:"canChat" example:"true"`
	CanEmbed      bool      `json:"canEmbed" example:"false"`
	CanPrompt     bool      `json:"canPrompt" example:"true"`
	CanStream     bool      `json:"canStream" example:"true"`
	CreatedAt     time.Time `json:"createdAt" example:"2023-11-15T14:30:45Z"`
	UpdatedAt     time.Time `json:"updatedAt" example:"2023-11-15T14:30:45Z"`
}

type AffinityGroup struct {
	ID          string `json:"id" example:"p9a8b7c6-d5e4-f3a2-b1c0-d9e8f7a6b5c4"`
	Name        string `json:"name" example:"production-chat"`
	PurposeType string `json:"purposeType" example:"Internal Tasks"`

	CreatedAt time.Time `json:"createdAt" example:"2023-11-15T14:30:45Z"`
	UpdatedAt time.Time `json:"updatedAt" example:"2023-11-15T14:30:45Z"`
}

type Job struct {
	ID           string          `json:"id" example:"j1a2b3c4-d5e6-f7g8-h9i0-j1k2l3m4n5o6"`
	TaskType     string          `json:"taskType" example:"model-download"`
	Payload      json.RawMessage `json:"payload" example:"{\"model\":\"mistral:instruct\",\"backend\":\"b7d9e1a3-8f0c-4a7d-9b1e-2f3a4b5c6d7e\"}"`
	ScheduledFor int64           `json:"scheduledFor" example:"1717020800"`
	ValidUntil   int64           `json:"validUntil" example:"1717024400"`
	RetryCount   int             `json:"retryCount" example:"0"`
	CreatedAt    time.Time       `json:"createdAt" example:"2023-11-15T14:30:45Z"`
}

type KV struct {
	Key       string          `json:"key" example:"config:default-model"`
	Value     json.RawMessage `json:"value" example:"\"mistral:instruct\""`
	CreatedAt time.Time       `json:"createdAt" example:"2023-11-15T14:30:45Z"`
	UpdatedAt time.Time       `json:"updatedAt" example:"2023-11-15T14:30:45Z"`
}

const (
	LocationPath  = "path"
	LocationQuery = "query"
	LocationBody  = "body"
)

type InjectionArg struct {
	Name  string `json:"name" example:"access_token"`
	Value any    `json:"value" example:"secret-token"`
	In    string `json:"in" example:"body"`
}

type AuthFlow struct {
	Type            string `json:"type" example:"http_handshake"`
	LoginMethod     string `json:"loginMethod" example:"POST"`
	LoginURL        string `json:"loginUrl" example:"https://erp.local/api/method/login"`
	LoginBody       string `json:"loginBody" example:"{\"usr\":\"${FRAPPE_USER}\", \"pwd\":\"${FRAPPE_PASS}\"}"`
	ExtractCookie   string `json:"extractCookie,omitempty" example:"sid"`
	ExtractJSONPath string `json:"extractJsonPath,omitempty" example:"$.data.token"`
	InjectHeader    string `json:"injectHeader,omitempty" example:"Cookie"`
	InjectFormat    string `json:"injectFormat,omitempty" example:"sid=%s"`
}

type RemoteTools struct {
	ID                 string            `json:"id" example:"h1a2b3c4-d5e6-f7g8-h9i0-j1k2l3m4n5o6"`
	Name               string            `json:"name" example:"mailing-tools"`
	EndpointURL        string            `json:"endpointUrl" example:"http://tools-endpoint:port"`
	SpecURL            string            `json:"specUrl,omitempty"`
	TimeoutMs          int               `json:"timeoutMs" example:"5000"`
	Headers            map[string]string `json:"headers,omitempty" example:"Authorization:Bearer token,Content-Type:application/json"`
	Properties         InjectionArg      `json:"properties"`
	InjectParams       map[string]string `json:"injectParams,omitempty"`
	AuthFlow           *AuthFlow         `json:"authFlow,omitempty"`
	InsecureSkipVerify bool              `json:"insecureSkipVerify"`
	CreatedAt          time.Time         `json:"createdAt" example:"2023-11-15T14:30:45Z"`
	UpdatedAt          time.Time         `json:"updatedAt" example:"2023-11-15T14:30:45Z"`
}

type Store interface {
	CreateBackend(ctx context.Context, backend *Backend) error
	CountBackendsByTypeAndURL(ctx context.Context, typ, baseURL, excludeID string) (int, error)
	GetBackend(ctx context.Context, id string) (*Backend, error)
	UpdateBackend(ctx context.Context, backend *Backend) error
	DeleteBackend(ctx context.Context, id string) error
	LockBackend(ctx context.Context, id string) error
	GetLLMProviderModelFacts(ctx context.Context, providerID string) (map[string]ModelFacts, error)
	SetLLMProviderModelFact(ctx context.Context, providerID, model string, facts ModelFacts) error
	ListAllBackends(ctx context.Context) ([]*Backend, error)
	ListBackends(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*Backend, error)
	GetBackendByName(ctx context.Context, name string) (*Backend, error)
	EstimateBackendCount(ctx context.Context) (int64, error)

	AppendModel(ctx context.Context, model *Model) error
	GetModel(ctx context.Context, id string) (*Model, error)
	GetModelByName(ctx context.Context, name string) (*Model, error)
	DeleteModel(ctx context.Context, modelName string) error
	ListAllModels(ctx context.Context) ([]*Model, error)
	UpdateModel(ctx context.Context, data *Model) error
	ListModels(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*Model, error)
	EstimateModelCount(ctx context.Context) (int64, error)

	CreateAffinityGroup(ctx context.Context, group *AffinityGroup) error
	GetAffinityGroup(ctx context.Context, id string) (*AffinityGroup, error)
	GetAffinityGroupByName(ctx context.Context, name string) (*AffinityGroup, error)
	UpdateAffinityGroup(ctx context.Context, group *AffinityGroup) error
	DeleteAffinityGroup(ctx context.Context, id string) error
	ListAllAffinityGroups(ctx context.Context) ([]*AffinityGroup, error)
	ListAffinityGroups(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*AffinityGroup, error)
	ListAffinityGroupByPurpose(ctx context.Context, purposeType string, createdAtCursor *time.Time, limit int, afterID ...string) ([]*AffinityGroup, error)
	EstimateAffinityGroupCount(ctx context.Context) (int64, error)

	AssignBackendToAffinityGroup(ctx context.Context, groupID string, backendID string) error
	RemoveBackendFromAffinityGroup(ctx context.Context, groupID string, backendID string) error
	ListBackendsForAffinityGroup(ctx context.Context, groupID string) ([]*Backend, error)
	ListAffinityGroupsForBackend(ctx context.Context, backendID string) ([]*AffinityGroup, error)

	AssignModelToAffinityGroup(ctx context.Context, groupID string, modelID string) error
	RemoveModelFromAffinityGroup(ctx context.Context, groupID string, modelID string) error
	ListModelsForAffinityGroup(ctx context.Context, groupID string) ([]*Model, error)
	ListAffinityGroupsForModel(ctx context.Context, modelID string) ([]*AffinityGroup, error)

	AppendJob(ctx context.Context, job Job) error
	AppendJobs(ctx context.Context, jobs ...*Job) error
	PopAllJobs(ctx context.Context) ([]*Job, error)
	PopJobsForType(ctx context.Context, taskType string) ([]*Job, error)
	PopNJobsForType(ctx context.Context, taskType string, n int) ([]*Job, error)
	PopJobForType(ctx context.Context, taskType string) (*Job, error)
	GetJobsForType(ctx context.Context, taskType string) ([]*Job, error)
	ListJobs(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*Job, error)
	EstimateJobCount(ctx context.Context) (int64, error)

	SetKV(ctx context.Context, key string, value json.RawMessage) error
	UpdateKV(ctx context.Context, key string, value json.RawMessage) error
	GetKV(ctx context.Context, key string, out interface{}) error
	DeleteKV(ctx context.Context, key string) error
	ListKV(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*KV, error)
	ListKVPrefix(ctx context.Context, prefix string, createdAtCursor *time.Time, limit int, afterID ...string) ([]*KV, error)
	EstimateKVCount(ctx context.Context) (int64, error)
	SetWorkspaceKV(ctx context.Context, workspaceID string, key string, value json.RawMessage) error
	GetWorkspaceKV(ctx context.Context, workspaceID string, key string, out interface{}) error
	DeleteWorkspaceKV(ctx context.Context, workspaceID string, key string) error

	CreateRemoteTools(ctx context.Context, tools *RemoteTools) error
	GetRemoteTools(ctx context.Context, id string) (*RemoteTools, error)
	GetRemoteToolsByName(ctx context.Context, name string) (*RemoteTools, error)
	UpdateRemoteTools(ctx context.Context, tools *RemoteTools) error
	DeleteRemoteTools(ctx context.Context, id string) error
	ListRemoteTools(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*RemoteTools, error)
	EstimateRemoteToolsCount(ctx context.Context) (int64, error)

	CreateModelRegistryEntry(ctx context.Context, e *ModelRegistryEntry) error
	GetModelRegistryEntry(ctx context.Context, id string) (*ModelRegistryEntry, error)
	GetModelRegistryEntryByName(ctx context.Context, name string) (*ModelRegistryEntry, error)
	UpdateModelRegistryEntry(ctx context.Context, e *ModelRegistryEntry) error
	DeleteModelRegistryEntry(ctx context.Context, id string) error
	ListModelRegistryEntries(ctx context.Context, cursor *time.Time, limit int, afterID ...string) ([]*ModelRegistryEntry, error)
	EstimateModelRegistryEntryCount(ctx context.Context) (int64, error)

	CreateMCPServer(ctx context.Context, srv *MCPServer) error
	GetMCPServer(ctx context.Context, id string) (*MCPServer, error)
	GetMCPServerByName(ctx context.Context, name string) (*MCPServer, error)
	UpdateMCPServer(ctx context.Context, srv *MCPServer) error
	DeleteMCPServer(ctx context.Context, id string) error
	ListMCPServers(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*MCPServer, error)
	EstimateMCPServerCount(ctx context.Context) (int64, error)
	UpsertMCPServerByName(ctx context.Context, srv *MCPServer) error

	CreateAgent(ctx context.Context, agent *Agent) error
	GetAgent(ctx context.Context, id string) (*Agent, error)
	GetAgentByName(ctx context.Context, name string) (*Agent, error)
	UpdateAgent(ctx context.Context, agent *Agent) error
	DeleteAgent(ctx context.Context, id string) error
	ListAgents(ctx context.Context, createdAtCursor *time.Time, limit int, afterID ...string) ([]*Agent, error)
	EstimateAgentCount(ctx context.Context) (int64, error)

	CreateHITLApproval(ctx context.Context, a *HITLApproval) error
	GetHITLApproval(ctx context.Context, id string) (*HITLApproval, error)
	ResolveHITLApproval(ctx context.Context, id string, state HITLApprovalState, resolution json.RawMessage, resolvedAt time.Time) error
	ListExpiredHITLApprovals(ctx context.Context, asOf time.Time, limit int) ([]*HITLApproval, error)
	ListHITLApprovals(ctx context.Context, state HITLApprovalState, createdAtCursor *time.Time, limit int, afterID ...string) ([]*HITLApproval, error)
	ListHITLApprovalsForMission(ctx context.Context, missionID string, limit int) ([]*HITLApproval, error)
	EstimateHITLApprovalCount(ctx context.Context) (int64, error)

	EnforceMaxRowCount(ctx context.Context, count int64) error
}

//go:embed schema.sql
var Schema string

//go:embed schema_sqlite.sql
var SchemaSQLite string

type store struct {
	libdb.Exec
}

func New(exec libdb.Exec) Store {
	if exec == nil {
		panic("SERVER BUG: store.New called with nil exec")
	}
	return &store{exec}
}

const MaxRowsCount = 100000

var sqliteCountableTables = map[string]bool{
	"job_queue_v2": true, "kv": true, "remote_tools": true,
	"ollama_models": true, "llm_affinity_group": true, "llm_backends": true,
	"mcp_servers": true, "llm_model_registry": true, "agents": true,
	"hitl_approvals": true,
}

func (s *store) estimateCount(ctx context.Context, table string) (int64, error) {
	var count int64
	err := s.Exec.QueryRowContext(ctx, `
		SELECT estimate_row_count($1)
	`, table).Scan(&count)
	if err == nil {
		return count, nil
	}
	if !strings.Contains(err.Error(), "no such function") {
		return 0, err
	}
	if !sqliteCountableTables[table] {
		return 0, err
	}
	err = s.Exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count)
	return count, err
}

func (s *store) EnforceMaxRowCount(ctx context.Context, count int64) error {
	if count >= MaxRowsCount {
		return fmt.Errorf("row limit reached (max %d)", MaxRowsCount)
	}
	return nil
}

func quiet() func() {
	null, _ := os.Open(os.DevNull)
	sout := os.Stdout
	serr := os.Stderr
	os.Stdout = null
	os.Stderr = null
	log.SetOutput(null)
	return func() {
		defer null.Close()
		os.Stdout = sout
		os.Stderr = serr
		log.SetOutput(os.Stderr)
	}
}

// EachStore runs fn as a contract test against every database the store
// supports: SQLite (always: it is what the desktop app ships) and Postgres
// (when a Docker daemon can provide a disposable instance; otherwise that
// subtest is skipped with a reason, never silently dropped).
func EachStore(t *testing.T, fn func(t *testing.T, ctx context.Context, s Store)) {
	t.Helper()
	EachDB(t, func(t *testing.T, ctx context.Context, exec libdb.Exec) {
		fn(t, ctx, New(exec))
	})
}

// EachDB is EachStore for tests that also need the raw executor (for example
// to force identical timestamps).
func EachDB(t *testing.T, fn func(t *testing.T, ctx context.Context, exec libdb.Exec)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		ctx, exec := setupSQLite(t)
		fn(t, ctx, exec)
	})
	t.Run("postgres", func(t *testing.T) {
		ctx, exec := setupPostgres(t)
		fn(t, ctx, exec)
	})
}

// setupSQLite opens a fresh on-disk SQLite database with the shipped schema,
// removed when the test ends.
func setupSQLite(t *testing.T) (context.Context, libdb.Exec) {
	t.Helper()
	ctx := context.TODO()
	dbManager, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "store.db"), SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, dbManager.Close()) })
	return ctx, dbManager.WithoutTransaction()
}

// setupPostgres starts a disposable Postgres instance, or skips the test when
// Docker is not available on this host.
func setupPostgres(t *testing.T) (context.Context, libdb.Exec) {
	t.Helper()
	if !localPostgresAvailable() {
		t.Skip("Postgres contract run needs a Docker daemon for the disposable instance; not available on this host (the SQLite run still covers the same assertions)")
	}

	unquiet := quiet()
	t.Cleanup(unquiet)

	ctx := context.TODO()
	connStr, _, cleanup, err := libdb.SetupLocalInstance(ctx, "test", "test", "test")
	require.NoError(t, err)

	dbManager, err := libdb.NewPostgresDBManager(ctx, connStr, Schema)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, dbManager.Close())
		cleanup()
	})

	return ctx, dbManager.WithoutTransaction()
}

// localPostgresAvailable reports whether a Docker daemon can provide the
// disposable Postgres instance the store tests use.
func localPostgresAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	cmd := exec.Command("docker", "info")
	winproc.Hide(cmd)
	return cmd.Run() == nil
}

// keysetBefore builds the WHERE condition for the page that follows a cursor
// in "ORDER BY <timeCol> DESC, <idCol> DESC" listings. The cursor is the last
// row of the previous page: its created_at, plus (optionally) its id.
//
// With an id the condition is a real keyset
//
//	timeCol < t OR (timeCol = t AND idCol < id)
//
// so rows sharing a timestamp with the last row of a page are neither skipped
// nor repeated. Without an id it degrades to "timeCol < t", which is only safe
// when created_at is unique; callers that page should pass the id.
//
// The cursor is normalised to UTC because SQLite stores time.Time as text in
// Go's format ("2006-01-02 15:04:05.999999999 -0700 MST"); every writer in this
// package stores UTC, so text comparison orders the same as time comparison
// (the format trims trailing zeros, and ' ' sorts before '.' and digits, so a
// shorter fraction of the same instant prefix always sorts first). Postgres
// compares real timestamps. argN is the 1-based index of the first placeholder
// the condition uses. It returns "" and no args when cursor is nil (first page).
func keysetBefore(timeCol, idCol string, cursor *time.Time, afterID []string, argN int) (string, []any) {
	if cursor == nil {
		return "", nil
	}
	t := cursor.UTC()
	id := ""
	if len(afterID) > 0 {
		id = afterID[0]
	}
	if id == "" {
		return fmt.Sprintf("%s < $%d", timeCol, argN), []any{t}
	}
	return fmt.Sprintf("(%s < $%d OR (%s = $%d AND %s < $%d))", timeCol, argN, timeCol, argN, idCol, argN+1), []any{t, id}
}

// whereAnd joins non-empty conditions into a WHERE clause ("" when none).
func whereAnd(conds ...string) string {
	var parts []string
	for _, c := range conds {
		if c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(parts, " AND ")
}
