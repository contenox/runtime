package vectors

import (
	"context"
	"errors"
	"time"

	"github.com/vdaas/vald-client-go/v1/payload"
	"github.com/vdaas/vald-client-go/v1/vald"
	"google.golang.org/grpc"
)

type valdStore struct {
	conn   *grpc.ClientConn
	client vald.Client
}

type Store interface {
	Insert(ctx context.Context, vector Vector) error
	Upsert(ctx context.Context, vector Vector) error
	BatchInsert(ctx context.Context, vectors []Vector) error
	Get(ctx context.Context, id string) (*Vector, error)
	Search(ctx context.Context, query []float32, k int) ([]VectorSearchResult, error)
	Delete(ctx context.Context, id string) error
}

func New(ctx context.Context, addr string) (Store, func() error, error) {
	// Create a Vald client connection for Vald cluster.
	close := func() error {
		return nil
	}
	conn, err := grpc.NewClient(addr)
	if err != nil {
		return nil, close, err
	}

	// Creates Vald client for gRPC.
	client := vald.NewValdClient(conn)
	close = conn.Close
	return &valdStore{
		conn:   conn,
		client: client,
	}, close, nil
}

type Vector struct {
	ID   string
	Data []float32
}

func (vs *valdStore) Insert(ctx context.Context, v Vector) error {
	_, err := vs.client.Insert(ctx, &payload.Insert_Request{
		Vector: &payload.Object_Vector{
			Id:        v.ID,
			Vector:    v.Data,
			Timestamp: time.Now().UTC().Unix(),
		},
	})
	if err != nil {
		return err
	}
	return nil
}

func (vs *valdStore) Upsert(ctx context.Context, v Vector) error {
	_, err := vs.client.Upsert(ctx, &payload.Upsert_Request{
		Vector: &payload.Object_Vector{
			Id:        v.ID,
			Vector:    v.Data,
			Timestamp: time.Now().UTC().Unix(),
		},
	})
	return err
}

func (vs *valdStore) BatchInsert(ctx context.Context, vectors []Vector) error {
	reqs := make([]*payload.Insert_Request, 0, len(vectors))
	for _, v := range vectors {
		reqs = append(reqs, &payload.Insert_Request{
			Vector: &payload.Object_Vector{
				Id:        v.ID,
				Vector:    v.Data,
				Timestamp: time.Now().UTC().Unix(),
			},
		})
	}
	_, err := vs.client.MultiInsert(ctx, &payload.Insert_MultiRequest{
		Requests: reqs,
	})
	return err
}

// Get fetches a single vector by ID via the Object service.
func (vs *valdStore) Get(ctx context.Context, id string) (*Vector, error) {
	resp, err := vs.client.GetObject(ctx, &payload.Object_VectorRequest{
		Id: &payload.Object_ID{Id: id},
	})
	if err != nil {
		return nil, err
	}
	return &Vector{
		ID:   resp.GetId(),
		Data: resp.GetVector(),
	}, nil
}

type VectorSearchResult struct {
	ID       string
	Distance float32
}

func (vs *valdStore) Search(ctx context.Context, query []float32, num int) ([]VectorSearchResult, error) {
	res, err := vs.client.Search(ctx, &payload.Search_Request{
		Vector: query,
		Config: &payload.Search_Config{
			Num:     uint32(num),
			MinNum:  1,
			Radius:  0.0,
			Epsilon: 0.01,
			Timeout: int64(2 * time.Second),
		},
	})
	if err != nil {
		return nil, err
	}

	if res == nil || len(res.Results) == 0 {
		return nil, errors.New("no results found")
	}

	results := make([]VectorSearchResult, 0, len(res.Results))
	for _, r := range res.Results {
		results = append(results, VectorSearchResult{
			ID:       r.Id,
			Distance: r.Distance,
		})
	}
	return results, nil
}

func (vs *valdStore) Delete(ctx context.Context, id string) error {
	_, err := vs.client.Remove(ctx, &payload.Remove_Request{
		Id: &payload.Object_ID{Id: id},
	})
	return err
}
