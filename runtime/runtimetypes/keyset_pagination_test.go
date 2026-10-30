package runtimetypes_test

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// pageRow is the cursor of one listed row: its created_at and the id (or key)
// that breaks ties.
type pageRow struct {
	createdAt time.Time
	id        string
}

// keysetCase describes one list method that pages with (created_at, id).
type keysetCase struct {
	name  string
	table string
	// idColumn is the tie-break column of the table.
	idColumn string
	// create inserts the i-th row and returns its id.
	create func(ctx context.Context, s runtimetypes.Store, i int) (string, error)
	// list returns one page after the cursor (nil cursor = first page).
	list func(ctx context.Context, s runtimetypes.Store, cursor *time.Time, afterID string, limit int) ([]pageRow, error)
}

func keysetCases() []keysetCase {
	return []keysetCase{
		{
			name: "backends", table: "llm_backends", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				b := &runtimetypes.Backend{ID: uuid.NewString(), Name: fmt.Sprintf("b-%d", i), BaseURL: fmt.Sprintf("http://h%d", i), Type: "ollama"}
				return b.ID, s.CreateBackend(ctx, b)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListBackends(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "agents", table: "agents", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				a := newExternalACPAgent(fmt.Sprintf("a-%d", i))
				return a.ID, s.CreateAgent(ctx, a)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListAgents(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "affinity_groups", table: "llm_affinity_group", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				g := &runtimetypes.AffinityGroup{ID: uuid.NewString(), Name: fmt.Sprintf("g-%d", i), PurposeType: "chat"}
				return g.ID, s.CreateAffinityGroup(ctx, g)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListAffinityGroups(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "affinity_groups_by_purpose", table: "llm_affinity_group", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				g := &runtimetypes.AffinityGroup{ID: uuid.NewString(), Name: fmt.Sprintf("gp-%d", i), PurposeType: "chat"}
				return g.ID, s.CreateAffinityGroup(ctx, g)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListAffinityGroupByPurpose(ctx, "chat", c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "models", table: "ollama_models", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				m := &runtimetypes.Model{ID: uuid.NewString(), Model: fmt.Sprintf("m-%d", i), ContextLength: 1024, CanChat: true}
				return m.ID, s.AppendModel(ctx, m)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListModels(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "remote_tools", table: "remote_tools", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				rt := &runtimetypes.RemoteTools{
					ID: uuid.NewString(), Name: fmt.Sprintf("rt-%d", i), EndpointURL: "https://example.com/tools", TimeoutMs: 1000,
					Properties: runtimetypes.InjectionArg{Name: "p", Value: "v", In: "body"},
				}
				return rt.ID, s.CreateRemoteTools(ctx, rt)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListRemoteTools(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "mcp_servers", table: "mcp_servers", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				m := newSSE(fmt.Sprintf("mcp-%d", i))
				return m.ID, s.CreateMCPServer(ctx, m)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListMCPServers(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "model_registry", table: "llm_model_registry", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				e := &runtimetypes.ModelRegistryEntry{ID: uuid.NewString(), Name: fmt.Sprintf("reg-%d", i), SourceURL: "https://example.com/m.gguf"}
				return e.ID, s.CreateModelRegistryEntry(ctx, e)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListModelRegistryEntries(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "jobs", table: "job_queue_v2", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				j := runtimetypes.Job{ID: uuid.NewString(), TaskType: "t", Payload: []byte(`{}`)}
				return j.ID, s.AppendJob(ctx, j)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListJobs(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
		{
			name: "kv", table: "kv", idColumn: "key",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				k := fmt.Sprintf("pfx:key-%02d", i)
				return k, s.SetKV(ctx, k, []byte(`1`))
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListKV(ctx, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.Key})
				}
				return out, err
			},
		},
		{
			name: "kv_prefix", table: "kv", idColumn: "key",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				k := fmt.Sprintf("pfx:key-%02d", i)
				return k, s.SetKV(ctx, k, []byte(`1`))
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListKVPrefix(ctx, "pfx:", c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.Key})
				}
				return out, err
			},
		},
		{
			name: "hitl_approvals", table: "hitl_approvals", idColumn: "id",
			create: func(ctx context.Context, s runtimetypes.Store, i int) (string, error) {
				a := newPendingApproval()
				return a.ID, s.CreateHITLApproval(ctx, a)
			},
			list: func(ctx context.Context, s runtimetypes.Store, c *time.Time, id string, n int) ([]pageRow, error) {
				items, err := s.ListHITLApprovals(ctx, runtimetypes.HITLApprovalPending, c, n, id)
				var out []pageRow
				for _, it := range items {
					out = append(out, pageRow{it.CreatedAt, it.ID})
				}
				return out, err
			},
		},
	}
}

// Rows that share one created_at instant must each come back exactly once when
// paged with a small page size, in (created_at, id) descending order, on every
// database the store supports. Before keyset pagination, rows tied with the
// last row of a page were skipped by the next page.
func TestUnit_ListPagination_IdenticalCreatedAt_ReturnsEveryRowOnce(t *testing.T) {
	const rows = 7
	const pageSize = 3

	timestamps := map[string]time.Time{
		// 7 fractional digits, like the owner's database ("... 11:18:45.0530663 +0000 UTC").
		"fractional": time.Date(2026, 10, 30, 11, 18, 45, 53066300, time.UTC),
		// Whole second: the stored text has no fraction at all.
		"whole_second": time.Date(2026, 10, 30, 11, 18, 45, 0, time.UTC),
	}

	runtimetypes.EachDB(t, func(t *testing.T, ctx context.Context, exec libdb.Exec) {
		for tsName, ts := range timestamps {
			if exec.DriverName() == "postgres" {
				// Postgres timestamps have microsecond precision.
				ts = ts.Truncate(time.Microsecond)
			}
			for _, tc := range keysetCases() {
				t.Run(tsName+"/"+tc.name, func(t *testing.T) {
					s := runtimetypes.New(exec)
					// Start from an empty table (cases share tables, and kv is shared).
					_, err := exec.ExecContext(ctx, "DELETE FROM "+tc.table)
					require.NoError(t, err)

					for i := 0; i < rows; i++ {
						_, err := tc.create(ctx, s, i)
						require.NoError(t, err)
					}
					// Force every row onto one instant.
					_, err = exec.ExecContext(ctx, "UPDATE "+tc.table+" SET created_at = $1", ts)
					require.NoError(t, err)

					// Expected order: the id column sorted by the database itself, so
					// the check uses the same collation as the keyset comparison.
					want := scanStrings(t, ctx, exec, "SELECT "+tc.idColumn+" FROM "+tc.table+" ORDER BY "+tc.idColumn+" DESC")
					require.Len(t, want, rows)

					var got []string
					var cursor *time.Time
					var afterID string
					for pages := 0; ; pages++ {
						require.Less(t, pages, rows+2, "paging must terminate")
						page, err := tc.list(ctx, s, cursor, afterID, pageSize)
						require.NoError(t, err)
						if len(page) == 0 {
							break
						}
						require.LessOrEqual(t, len(page), pageSize)
						for _, r := range page {
							require.True(t, r.createdAt.Equal(ts), "row created_at %v want %v", r.createdAt, ts)
							got = append(got, r.id)
						}
						last := page[len(page)-1]
						cursor = &last.createdAt
						afterID = last.id
					}
					require.Equal(t, want, got, "every row exactly once, ordered by id descending")
				})
			}
		}
	})
}

// A cursor without an id keeps working (created_at < cursor) for callers that
// have only the timestamp.
func TestUnit_ListPagination_TimeOnlyCursorStillPages(t *testing.T) {
	runtimetypes.EachStore(t, func(t *testing.T, ctx context.Context, s runtimetypes.Store) {
		for i := 0; i < 3; i++ {
			require.NoError(t, s.CreateBackend(ctx, &runtimetypes.Backend{ID: uuid.NewString(), Name: fmt.Sprintf("b%d", i), BaseURL: fmt.Sprintf("http://h%d", i), Type: "ollama"}))
			tick()
		}
		first, err := s.ListBackends(ctx, nil, 1)
		require.NoError(t, err)
		require.Len(t, first, 1)
		rest, err := s.ListBackends(ctx, &first[0].CreatedAt, 10)
		require.NoError(t, err)
		require.Len(t, rest, 2)
	})
}

func scanStrings(t *testing.T, ctx context.Context, exec libdb.Exec, query string) []string {
	t.Helper()
	rows, err := exec.QueryContext(ctx, query)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		out = append(out, v)
	}
	require.NoError(t, rows.Err())
	return out
}

// SQLite stores time.Time as text with trailing fractional zeros trimmed, so
// instants with different fraction lengths must still compare in time order
// through the cursor.
func TestUnit_ListPagination_MixedFractionLengthsKeepTimeOrder(t *testing.T) {
	base := time.Date(2026, 10, 30, 11, 18, 45, 0, time.UTC)
	instants := []time.Time{
		base,                            // ...:45
		base.Add(50 * time.Millisecond), // ...:45.05
		base.Add(500 * time.Millisecond),
		base.Add(53066300 * time.Nanosecond), // ...:45.0530663
		base.Add(time.Second),                // ...:46
		base.Add(-time.Second),               // ...:44
	}
	runtimetypes.EachDB(t, func(t *testing.T, ctx context.Context, exec libdb.Exec) {
		s := runtimetypes.New(exec)
		ids := make([]string, len(instants))
		for i, ts := range instants {
			b := &runtimetypes.Backend{ID: uuid.NewString(), Name: fmt.Sprintf("b%d", i), BaseURL: fmt.Sprintf("http://h%d", i), Type: "ollama"}
			require.NoError(t, s.CreateBackend(ctx, b))
			ids[i] = b.ID
			_, err := exec.ExecContext(ctx, "UPDATE llm_backends SET created_at = $1 WHERE id = $2", ts, b.ID)
			require.NoError(t, err)
		}
		order := make([]int, len(instants))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return instants[order[a]].After(instants[order[b]]) })

		var got []string
		var cursor *time.Time
		var afterID string
		for {
			page, err := s.ListBackends(ctx, cursor, 2, afterID)
			require.NoError(t, err)
			if len(page) == 0 {
				break
			}
			for _, b := range page {
				got = append(got, b.ID)
			}
			cursor = &page[len(page)-1].CreatedAt
			afterID = page[len(page)-1].ID
		}
		var want []string
		for _, i := range order {
			want = append(want, ids[i])
		}
		require.Equal(t, want, got)
	})
}
