package indexapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/services/indexservice"
)

func AddIndexRoutes(mux *http.ServeMux, _ *serverops.Config, indexService indexservice.Service) {
	f := &indexManager{
		service: indexService,
	}
	mux.HandleFunc("POST /index", f.index)
	mux.HandleFunc("GET /search", f.search)
}

type indexManager struct {
	service indexservice.Service
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
	q, err := url.QueryUnescape(r.URL.Query().Get("q"))
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}
	k := 0
	var radius *float32
	var epsilon *float32

	if r.URL.Query().Get("topk") != "" {
		topK, err := url.QueryUnescape(r.URL.Query().Get("topk"))
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		k64, err := strconv.ParseInt(topK, 10, 32)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		k = int(k64)
	}
	if r.URL.Query().Get("radius") != "" {
		rad, err := url.QueryUnescape(r.URL.Query().Get("radius"))
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		radiusV, err := strconv.ParseFloat(rad, 32)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		t := float32(radiusV)
		radius = &t
	}
	if r.URL.Query().Get("epsilon") != "" {
		p, err := url.QueryUnescape(r.URL.Query().Get("epsilon"))
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		convEpsilon, err := strconv.ParseFloat(p, 32)
		if err != nil {
			_ = serverops.Error(w, r, err, serverops.GetOperation)
			return
		}
		t := float32(convEpsilon)
		epsilon = &t
	}

	var args *indexservice.SearchRequestArgs
	if radius != nil && epsilon != nil {
		args = &indexservice.SearchRequestArgs{Radius: *radius, Epsilon: *epsilon}
	}
	req := indexservice.SearchRequest{Query: q, TopK: k,
		SearchRequestArgs: args,
	}
	if r.URL.Query().Get("expand") == "files" {
		req.ExpandFiles = true
	}
	resp, err := im.service.Search(r.Context(), &req)
	if err != nil {
		_ = serverops.Error(w, r, err, serverops.GetOperation)
		return
	}
	_ = serverops.Encode(w, r, http.StatusOK, resp)
}
