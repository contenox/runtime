package indexapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/services/indexservice"
)

func AddIndexRoutes(mux *http.ServeMux, _ *serverops.Config, indexService *indexservice.Service) {
	f := &indexManager{
		service: indexService,
	}
	mux.HandleFunc("POST /index", f.index)
	mux.HandleFunc("GET /search", f.search)
}

type indexManager struct {
	service *indexservice.Service
}

func (im *indexManager) index(w http.ResponseWriter, r *http.Request) {
	req, err := serverops.Decode[indexservice.IndexRequest](r)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}

	resp, err := im.service.Index(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.CreateOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}

func (im *indexManager) search(w http.ResponseWriter, r *http.Request) {
	q := url.QueryEscape(r.URL.Query().Get("q"))
	k := 0
	var radius *float32
	var epsilon *float32
	var err error
	if url.QueryEscape(r.URL.Query().Get("topk")) != "" {
		topK := url.QueryEscape(r.URL.Query().Get("topk"))
		k64, err := strconv.ParseInt(topK, 10, 32)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		k = int(k64)
	}
	if url.QueryEscape(r.URL.Query().Get("radius")) != "" {
		rad := url.QueryEscape(r.URL.Query().Get("radius"))
		radiusV, err := strconv.ParseFloat(rad, 32)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		t := float32(radiusV)
		radius = &t
	}
	if url.QueryEscape(r.URL.Query().Get("epsilon")) != "" {
		p := url.QueryEscape(r.URL.Query().Get("epsilon"))
		convEpsilon, err := strconv.ParseFloat(p, 32)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		t := float32(convEpsilon)
		epsilon = &t
	}
	// TODO
	var args *indexservice.SearchRequestArgs
	if radius != nil && epsilon != nil {
		args = &indexservice.SearchRequestArgs{Radius: *radius, Epsilon: *epsilon}
	}
	req := indexservice.SearchRequest{Query: q, TopK: k,
		SearchRequestArgs: args,
	}

	resp, err := im.service.Search(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}
