package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/codec/chatcompletions"
)

type OpenAIStreamClient struct {
	openAIClient
}

func (c *OpenAIStreamClient) Stream(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (<-chan *modelrepo.StreamParcel, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "stream", c.providerType(), "model", c.modelName)

	if err := modelrepo.RefuseAudioInput(c.providerType(), c.modelName, messages); err != nil {
		reportErr(err)
		end()
		return nil, err
	}

	streamCh := make(chan *modelrepo.StreamParcel)
	usesResponses := c.codex != nil || openAIUsesResponsesEndpoint(c.modelName, chatArgsHaveTools(args))
	endpoint := "/chat/completions"
	var requestBody []byte
	var nameMap map[string]string
	var err error

	if usesResponses {
		var req openAIResponsesRequest
		req, nameMap, err = c.buildResponsesRequest(messages, args)
		if err != nil {
			end()
			return nil, err
		}
		req.Stream = true
		requestBody, err = json.Marshal(req)
		endpoint = "/responses"
	} else {
		var req openAIChatRequest
		req, nameMap, err = buildOpenAIRequestForDialect(c.dialect, c.modelName, messages, args, c.thinking, c.supportsThink)
		if err != nil {
			end()
			return nil, err
		}
		req.Stream = true
		req.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
		c.clampChatMaxOutputTokens(&req)
		requestBody, err = c.chatRequestBody(req, args)
	}
	if err != nil {
		end()
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	var resp *http.Response
	if c.codex != nil {
		resp, err = c.codex.request(ctx, http.MethodPost, endpoint, requestBody)
	} else {
		httpReq, createErr := http.NewRequestWithContext(ctx, "POST", c.baseURL+endpoint, bytes.NewBuffer(requestBody))
		if createErr != nil {
			end()
			return nil, fmt.Errorf("failed to create request: %w", createErr)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err = c.httpClient.Do(httpReq)
	}
	if err != nil {
		err = fmt.Errorf("HTTP request failed for model %s: %w", c.modelName, err)
		reportErr(err)
		end()
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		code, message := modelrepo.ProviderErrorDetail(body)
		err = fmt.Errorf("%s API returned non-200 status: %d for model %s: %s", c.vendorLabel(), resp.StatusCode, c.modelName, message)
		err = modelrepo.ClassifyProviderError(err, resp.StatusCode, code, message)
		reportErr(err)
		end()
		return nil, err
	}

	go func() {
		defer close(streamCh)
		defer resp.Body.Close()
		defer end()

		if usesResponses {
			streamResponsesSSE(ctx, resp.Body, nameMap, streamCh, reportErr, reportChange, c.responseContinuation)
			return
		}

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
			parcels, derr := dec.DecodeLine([]byte(jsonData))
			if derr != nil {
				derr = fmt.Errorf("failed to decode SSE data: %w, raw: %s", derr, jsonData)
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
			err = fmt.Errorf("stream scanning error: %w", err)
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

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

type responsesSSEEvent struct {
	Type        string                    `json:"type"`
	Delta       string                    `json:"delta"`
	Arguments   string                    `json:"arguments"`
	ItemID      string                    `json:"item_id"`
	OutputIndex int                       `json:"output_index"`
	Item        *openAIResponseOutputItem `json:"item"`
	Response    *openAIResponse           `json:"response"`
	Code        string                    `json:"code"`
	Message     string                    `json:"message"`
	Error       *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (ev responsesSSEEvent) errorText() string {
	code, msg := ev.Code, ev.Message
	if ev.Error != nil {
		if code == "" {
			code = ev.Error.Code
		}
		if msg == "" {
			msg = ev.Error.Message
		}
	}
	if ev.Response != nil && ev.Response.Error != nil {
		if code == "" {
			code = ev.Response.Error.Code
		}
		if msg == "" {
			msg = ev.Response.Error.Message
		}
	}
	if code == "" && msg == "" {
		if ev.Response != nil && ev.Response.IncompleteDetails != nil {
			return ev.Response.IncompleteDetails.Reason
		}
		return "no provider error detail"
	}
	return code + ": " + msg
}

type responsesToolCallState struct {
	index  int
	id     string
	itemID string
	name   string
	final  string
}

type responsesCallTracker struct {
	byIndex map[int]*responsesToolCallState
	byKey   map[string]*responsesToolCallState
	nameMap map[string]string
}

func newResponsesCallTracker(nameMap map[string]string) *responsesCallTracker {
	return &responsesCallTracker{
		byIndex: map[int]*responsesToolCallState{},
		byKey:   map[string]*responsesToolCallState{},
		nameMap: nameMap,
	}
}

func (t *responsesCallTracker) state(index int) *responsesToolCallState {
	if s, ok := t.byIndex[index]; ok {
		return s
	}
	s := &responsesToolCallState{index: index}
	t.byIndex[index] = s
	return s
}

func (t *responsesCallTracker) lookup(keys ...string) *responsesToolCallState {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if s, ok := t.byKey[key]; ok {
			return s
		}
	}
	return nil
}

func (t *responsesCallTracker) identity(index int, item *openAIResponseOutputItem) []*modelrepo.ToolCallDelta {
	s := t.lookup(item.CallID, item.ID)
	changed := false
	if s == nil {
		s = t.state(index)
		changed = true
	}
	if id := firstNonEmptyString(item.CallID, item.ID); id != "" && s.id == "" {
		s.id = id
		changed = true
	}
	if item.ID != "" && s.itemID == "" {
		s.itemID = item.ID
	}
	if item.Name != "" && s.name == "" {
		name := item.Name
		if orig, ok := t.nameMap[name]; ok && orig != "" {
			name = orig
		}
		s.name = name
		changed = true
	}
	for _, key := range []string{item.ID, item.CallID, s.id, s.itemID} {
		if key != "" {
			t.byKey[key] = s
		}
	}
	if !changed {
		return nil
	}
	return []*modelrepo.ToolCallDelta{{
		Index: s.index,
		ID:    s.id,
		Type:  "function",
		Name:  s.name,
	}}
}

func (t *responsesCallTracker) finalArguments(index int, itemID, arguments string) []*modelrepo.ToolCallDelta {
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	s := t.lookup(itemID)
	if s == nil {
		s = t.state(index)
	}
	if s.final == arguments {
		return nil
	}
	s.final = arguments
	return []*modelrepo.ToolCallDelta{{
		Index:        s.index,
		ArgsFragment: arguments,
		ArgsReplace:  true,
	}}
}

func (t *responsesCallTracker) reconcile(resp *openAIResponse) []*modelrepo.ToolCallDelta {
	if resp == nil {
		return nil
	}
	var out []*modelrepo.ToolCallDelta
	for i := range resp.Output {
		item := &resp.Output[i]
		if !strings.EqualFold(item.Type, "function_call") {
			continue
		}
		index := i
		if s := t.lookup(item.CallID, item.ID); s != nil {
			index = s.index
		}
		out = append(out, t.identity(index, item)...)
		out = append(out, t.finalArguments(index, item.ID, item.Arguments)...)
	}
	return out
}

func sendToolCallDeltas(send func(*modelrepo.StreamParcel) bool, deltas []*modelrepo.ToolCallDelta) bool {
	for _, d := range deltas {
		if !send(&modelrepo.StreamParcel{ToolCall: d}) {
			return false
		}
	}
	return true
}

func streamResponsesSSE(
	ctx context.Context,
	body io.ReadCloser,
	nameMap map[string]string,
	out chan<- *modelrepo.StreamParcel,
	reportErr func(error),
	reportChange func(string, any),
	continuation func(*openAIResponse) *modelrepo.Continuation,
) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var chunkCount int
	var emittedReasoning bool
	var refusalText strings.Builder

	send := func(p *modelrepo.StreamParcel) bool {
		select {
		case out <- p:
			return true
		case <-ctx.Done():
			return false
		}
	}

	calls := newResponsesCallTracker(nameMap)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var ev responsesSSEEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			err := fmt.Errorf("responses: malformed stream event")
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: err})
			return
		}

		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta == "" {
				continue
			}
			chunkCount++
			if !send(&modelrepo.StreamParcel{Data: ev.Delta}) {
				return
			}

		case "response.reasoning_summary_text.delta":
			if ev.Delta == "" {
				continue
			}
			emittedReasoning = true
			if !send(&modelrepo.StreamParcel{Thinking: ev.Delta}) {
				return
			}

		case "response.refusal.delta":
			if ev.Delta == "" {
				continue
			}
			refusalText.WriteString(ev.Delta)

		case "response.output_item.added":
			if ev.Item == nil || !strings.EqualFold(ev.Item.Type, "function_call") {
				continue
			}
			if !sendToolCallDeltas(send, calls.identity(ev.OutputIndex, ev.Item)) {
				return
			}

		case "response.function_call_arguments.delta":
			if ev.Delta == "" {
				continue
			}
			if !send(&modelrepo.StreamParcel{ToolCall: &modelrepo.ToolCallDelta{
				Index:        ev.OutputIndex,
				ArgsFragment: ev.Delta,
			}}) {
				return
			}

		case "response.function_call_arguments.done":
			if !sendToolCallDeltas(send, calls.finalArguments(ev.OutputIndex, ev.ItemID, ev.Arguments)) {
				return
			}

		case "response.output_item.done":
			if ev.Item == nil || !strings.EqualFold(ev.Item.Type, "function_call") {
				continue
			}
			if !sendToolCallDeltas(send, calls.identity(ev.OutputIndex, ev.Item)) {
				return
			}
			if !sendToolCallDeltas(send, calls.finalArguments(ev.OutputIndex, ev.Item.ID, ev.Item.Arguments)) {
				return
			}

		case "response.completed":
			if !sendToolCallDeltas(send, calls.reconcile(ev.Response)) {
				return
			}
			if refusal := firstNonEmptyString(refusalText.String(), responsesRefusalText(ev.Response)); refusal != "" {
				err := &modelrepo.RefusalError{Provider: "openai", Code: refusalFinishReason, Text: refusal}
				reportErr(err)
				send(&modelrepo.StreamParcel{Error: err})
				return
			}
			if !emittedReasoning {
				if summary := responsesReasoningSummaryText(ev.Response); summary != "" {
					if !send(&modelrepo.StreamParcel{Thinking: summary}) {
						return
					}
				}
			}
			term := &modelrepo.StreamTerminal{FinishReason: responsesFinishReason(ev.Response), Terminated: true}
			if continuation != nil {
				term.Continuation = continuation(ev.Response)
			}
			if ev.Response != nil {
				term.Usage = ev.Response.Usage.neutralUsage()
			}
			if !send(&modelrepo.StreamParcel{Terminal: term}) {
				return
			}
			reportChange("stream_completed", map[string]any{
				"path":        "responses",
				"chunk_count": chunkCount,
			})
			return

		case "response.incomplete":
			if refusal := firstNonEmptyString(refusalText.String(), responsesRefusalText(ev.Response)); refusal != "" {
				err := &modelrepo.RefusalError{Provider: "openai", Code: refusalFinishReason, Text: refusal}
				reportErr(err)
				send(&modelrepo.StreamParcel{Error: err})
				return
			}
			if !emittedReasoning {
				if summary := responsesReasoningSummaryText(ev.Response); summary != "" {
					if !send(&modelrepo.StreamParcel{Thinking: summary}) {
						return
					}
				}
			}
			term := &modelrepo.StreamTerminal{FinishReason: responsesFinishReason(ev.Response), Terminated: true}
			if continuation != nil {
				term.Continuation = continuation(ev.Response)
			}
			if ev.Response != nil {
				term.Usage = ev.Response.Usage.neutralUsage()
			}
			if !send(&modelrepo.StreamParcel{Terminal: term}) {
				return
			}
			reportChange("stream_incomplete", map[string]any{
				"path":        "responses",
				"chunk_count": chunkCount,
			})
			return

		case "error":
			err := modelrepo.ClassifyProviderError(fmt.Errorf("responses stream error %s", ev.errorText()), 0, ev.errorText(), ev.errorText())
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: err})
			return

		case "response.failed":
			err := modelrepo.ClassifyProviderError(fmt.Errorf("responses stream %s %s", ev.Type, ev.errorText()), 0, ev.errorText(), ev.errorText())
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: err})
			return
		}
	}

	if err := ctx.Err(); err != nil {
		reportErr(err)
		send(&modelrepo.StreamParcel{Error: err})
		return
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		err = fmt.Errorf("responses: stream read: %w", err)
		reportErr(err)
		send(&modelrepo.StreamParcel{Error: err})
		return
	}

	err := fmt.Errorf("responses: stream ended without response.completed")
	reportErr(err)
	send(&modelrepo.StreamParcel{Error: err})
}

var _ modelrepo.LLMStreamClient = (*OpenAIStreamClient)(nil)
