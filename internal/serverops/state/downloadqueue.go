package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/js402/CATE/internal/serverops/store"
	"github.com/js402/CATE/libs/libdb"

	"github.com/ollama/ollama/api"
)

type dwqueue struct {
	dbInstance libdb.DBManager
}

func (q dwqueue) add(ctx context.Context, u url.URL, models ...string) error {
	tx := q.dbInstance.WithoutTransaction()
	for _, model := range models {
		payload, err := json.Marshal(store.QueueItem{URL: u.String(), Model: model})
		if err != nil {
			return err
		}
		err = store.New(tx).AppendJob(ctx, store.Job{
			ID:       u.String(), // Using backends url as ID to prevent multiple downloads on the same backend
			TaskType: "model_download",
			Payload:  payload,
		})
		if err != nil {
			println(err)
		}
	}

	return nil
}

func (q dwqueue) pop(ctx context.Context) (*store.QueueItem, error) {
	tx := q.dbInstance.WithoutTransaction()

	job, err := store.New(tx).PopJobForType(ctx, "model_download")
	if err != nil {
		return nil, err
	}
	var item store.QueueItem
	// Use &item so json.Unmarshal writes into our allocated struct.
	err = json.Unmarshal(job.Payload, &item)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (q dwqueue) downloadModel(ctx context.Context, item store.QueueItem, progress func(status store.Status) error) error {
	u, err := url.Parse(item.URL)
	if err != nil {
		return err
	}
	client := api.NewClient(u, http.DefaultClient)

	err = client.Pull(ctx, &api.PullRequest{
		Model: item.Model,
	}, func(pr api.ProgressResponse) error {
		return progress(store.Status{
			Digest:    pr.Digest,
			Status:    pr.Status,
			Total:     pr.Total,
			Completed: pr.Completed,
			Model:     item.Model,
			BaseURL:   item.URL,
		})
	})
	if err != nil {
		return fmt.Errorf("failed to fetch %s: %v", item.URL, err)
	}
	return nil
}
