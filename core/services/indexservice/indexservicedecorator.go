package indexservice

import (
	"context"

	"github.com/contenox/activitytracker"
)

type activityTrackerDecorator struct {
	service Service
	tracker activitytracker.ActivityTracker
}

func (d *activityTrackerDecorator) Index(ctx context.Context, request *IndexRequest) (*IndexResponse, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"index",
		"document",
		"id", request.ID,
		"jobId", request.JobID,
		"leaserId", request.LeaserID,
	)
	defer endFn()

	resp, err := d.service.Index(ctx, request)
	if err != nil {
		reportErrFn(err)
	} else {
		reportChangeFn("indexed", map[string]interface{}{
			"id":         resp.ID,
			"vector_ids": resp.VectorIDs,
		})
	}

	return resp, err
}

func (d *activityTrackerDecorator) Search(ctx context.Context, request *SearchRequest) (*SearchResponse, error) {
	reportErrFn, reportChangeFn, endFn := d.tracker.Start(
		ctx,
		"search",
		"query",
		"text", request.Query,
		"topK", request.TopK,
	)
	defer endFn()

	resp, err := d.service.Search(ctx, request)
	if err != nil {
		reportErrFn(err)
	} else {
		resultsSummary := make([]map[string]interface{}, len(resp.Results))
		for i, r := range resp.Results {
			resultsSummary[i] = map[string]interface{}{
				"id":           r.ID,
				"distance":     r.Distance,
				"resourceType": r.ResourceType,
			}
		}

		reportChangeFn("search_results", map[string]interface{}{
			"results":      resultsSummary,
			"triedQueries": resp.TriedQueries,
		})
	}

	return resp, err
}

func (d *activityTrackerDecorator) ListKeywords(ctx context.Context) ([]string, error) {
	reportErrFn, _, endFn := d.tracker.Start(
		ctx,
		"list",
		"keywords",
	)
	defer endFn()

	keywords, err := d.service.ListKeywords(ctx)
	if err != nil {
		reportErrFn(err)
	}
	return keywords, err
}

func (d *activityTrackerDecorator) GetServiceName() string {
	return d.service.GetServiceName()
}

func (d *activityTrackerDecorator) GetServiceGroup() string {
	return d.service.GetServiceGroup()
}

// Wrap a Service with an activity tracker.
func WithActivityTracker(service Service, tracker activitytracker.ActivityTracker) Service {
	return &activityTrackerDecorator{
		service: service,
		tracker: tracker,
	}
}
