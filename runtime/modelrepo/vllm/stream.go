package vllm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/chatcompletions"
)

type VLLMStreamClient struct {
	vLLMClient
}

func NewVLLMStreamClient(ctx context.Context, baseURL, modelName string, contextLength, maxOutputTokens int, httpClient *http.Client, apiKey string, canThink, canAudio bool, tracker libtracker.ActivityTracker) (modelrepo.LLMStreamClient, error) {
	if httpClient == nil {
		httpClient = modelrepo.SharedHTTPClient
	}
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	client := &VLLMStreamClient{
		vLLMClient: vLLMClient{
			baseURL:         normalizeBaseURL(baseURL),
			httpClient:      httpClient,
			modelName:       modelName,
			maxOutputTokens: maxOutputTokens,
			canThink:        canThink,
			canAudio:        canAudio,
			apiKey:          apiKey,
			tracker:         tracker,
		},
	}

	client.maxTokens = min(contextLength, 2048)
	return client, nil
}

type streamErrorFrame struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param"`
		Code    int    `json:"code"`
	} `json:"error"`
	Object  string `json:"object"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    int    `json:"code"`
}

func streamError(jsonData string) (message, code string, status int, failed bool) {
	var frame streamErrorFrame
	if err := json.Unmarshal([]byte(jsonData), &frame); err != nil {
		return "", "", 0, false
	}
	if frame.Error != nil {
		return frame.Error.Message, frame.Error.Type, frame.Error.Code, true
	}
	if frame.Object == "error" && frame.Message != "" {
		return frame.Message, frame.Type, frame.Code, true
	}
	return "", "", 0, false
}

func (c *VLLMStreamClient) Stream(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (<-chan *modelrepo.StreamParcel, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "stream", "vllm", "model", c.modelName)

	if err := c.checkAudio(messages); err != nil {
		reportErr(err)
		end()
		return nil, err
	}

	request, nameMap := buildChatRequest(c.modelName, messages, args, c.canThink)
	c.clampChatRequest(&request)
	request.Stream = true
	request.StreamOptions = &streamOptions{IncludeUsage: true}

	url := c.baseURL + "/v1/chat/completions"
	reqBody, err := json.Marshal(request)
	if err != nil {
		end()
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		end()
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	streamCh := make(chan *modelrepo.StreamParcel)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		err = fmt.Errorf("HTTP request failed for model %s: %w", c.modelName, err)
		reportErr(err)
		end()
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		err = fmt.Errorf("vLLM API returned non-200 status: %d - %s for model %s",
			resp.StatusCode, string(body), c.modelName)
		err = modelrepo.ClassifyProviderError(err, resp.StatusCode, "", string(body))
		reportErr(err)
		end()
		return nil, err
	}

	go func() {
		defer close(streamCh)
		defer resp.Body.Close()
		defer end()

		send := func(p *modelrepo.StreamParcel) bool {
			select {
			case streamCh <- p:
				return true
			case <-ctx.Done():
				return false
			}
		}

		dec := chatcompletions.NewStreamDecoder(nameMap)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		var chunkCount int

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonData := strings.TrimPrefix(line, "data: ")
			if jsonData == "[DONE]" {
				dec.MarkTerminated()
				continue
			}

			if message, code, status, failed := streamError(jsonData); failed {
				err := modelrepo.ClassifyProviderError(fmt.Errorf("vLLM stream error: %s", message), status, code, message)
				reportErr(err)
				send(&modelrepo.StreamParcel{Error: err})
				return
			}

			parcels, derr := dec.DecodeLine([]byte(jsonData))
			if derr != nil {
				derr = fmt.Errorf("vLLM stream decode failed for model %s: %w", c.modelName, derr)
				reportErr(derr)
				send(&modelrepo.StreamParcel{Error: derr})
				return
			}
			for _, p := range parcels {
				chunkCount++
				if !send(p) {
					return
				}
				if p.Error != nil {
					reportErr(p.Error)
					return
				}
			}
		}

		if err := scanner.Err(); err != nil && err != io.EOF {
			err := fmt.Errorf("stream scanning error: %w", err)
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: err})
			return
		}

		if !send(dec.Finish()) {
			return
		}
		reportChange("stream_completed", map[string]any{"chunk_count": chunkCount})
	}()

	return streamCh, nil
}

var _ modelrepo.LLMStreamClient = (*VLLMStreamClient)(nil)
