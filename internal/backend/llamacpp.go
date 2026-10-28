// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// LlamaCppNative talks to a llama.cpp `llama-server` over its native endpoints
// (/props, /tokenize, /apply-template) and the OpenAI-compatible streaming chat
// endpoint. It is the only Backend implementation.
type LlamaCppNative struct {
	baseURL string
	token   string
	http    *http.Client
}

// Option configures a LlamaCppNative.
type Option func(*LlamaCppNative)

// WithHTTPClient sets the HTTP client (tests inject the fake server's client).
func WithHTTPClient(c *http.Client) Option { return func(l *LlamaCppNative) { l.http = c } }

// WithToken sets a bearer token for an auth-fronted reverse proxy.
func WithToken(t string) Option { return func(l *LlamaCppNative) { l.token = t } }

// New returns a LlamaCppNative for the given base URL (e.g.
// "http://localhost:8080" or "https://host/llama"); a trailing slash is fine.
func New(baseURL string, opts ...Option) *LlamaCppNative {
	l := &LlamaCppNative{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    http.DefaultClient,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

var _ Backend = (*LlamaCppNative)(nil)

// Props reads /props for the context size, model, and chat template.
func (l *LlamaCppNative) Props(ctx context.Context) (Props, error) {
	var raw struct {
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
		NCtxTop      int    `json:"n_ctx"`
		ModelPath    string `json:"model_path"`
		ChatTemplate string `json:"chat_template"`
	}
	if err := l.getJSON(ctx, "/props", &raw); err != nil {
		return Props{}, err
	}
	nctx := raw.DefaultGenerationSettings.NCtx
	if nctx == 0 {
		nctx = raw.NCtxTop
	}
	return Props{NCtx: nctx, ModelID: raw.ModelPath, ChatTemplate: raw.ChatTemplate}, nil
}

// Tokenize returns the token count for a rendered string via /tokenize.
func (l *LlamaCppNative) Tokenize(ctx context.Context, text string) (int, error) {
	var raw struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := l.postJSON(ctx, "/tokenize", map[string]any{"content": text}, &raw); err != nil {
		return 0, err
	}
	return len(raw.Tokens), nil
}

// ApplyTemplate renders messages through the model's chat template.
func (l *LlamaCppNative) ApplyTemplate(ctx context.Context, msgs []Message) (string, error) {
	var raw struct {
		Prompt string `json:"prompt"`
	}
	if err := l.postJSON(ctx, "/apply-template", map[string]any{"messages": msgs}, &raw); err != nil {
		return "", err
	}
	return raw.Prompt, nil
}

// ChatStream streams a completion over /v1/chat/completions. Content and
// reasoning stream as they arrive; streamed tool-call fragments are accumulated
// by index and delivered, assembled, on the terminal (Done) delta along with
// the authoritative usage.
func (l *LlamaCppNative) ChatStream(ctx context.Context, req ChatRequest, fn func(Delta) error) error {
	body := map[string]any{
		"messages":       req.Messages,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		body["tools"] = toolsWire(req.Tools)
	}
	resp, err := l.post(ctx, "/v1/chat/completions", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	acc := map[int]*ToolCall{}
	var order []int
	var usage *Usage
	emittedDone := false
	emitDone := func() error {
		if emittedDone {
			return nil
		}
		emittedDone = true
		d := Delta{Done: true, Usage: usage}
		sort.Ints(order)
		for _, idx := range order {
			d.ToolCalls = append(d.ToolCalls, *acc[idx])
		}
		return fn(d)
	}

	br := bufio.NewReader(resp.Body)
	for {
		line, readErr := br.ReadString('\n')
		if data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:"); ok {
			data = strings.TrimSpace(data)
			if data == "[DONE]" {
				if err := emitDone(); err != nil {
					return err
				}
				break
			}
			cd, perr := parseChatChunk(data)
			if perr != nil {
				return fmt.Errorf("llama: parse stream chunk: %w", perr)
			}
			if cd.content != "" || cd.reasoning != "" {
				if err := fn(Delta{Content: cd.content, Reasoning: cd.reasoning}); err != nil {
					return err
				}
			}
			for _, f := range cd.toolFrags {
				tc := acc[f.index]
				if tc == nil {
					tc = &ToolCall{Type: "function"}
					acc[f.index] = tc
					order = append(order, f.index)
				}
				if f.id != "" {
					tc.ID = f.id
				}
				if f.name != "" {
					tc.Function.Name = f.name
				}
				tc.Function.Arguments += f.args
			}
			if cd.usage != nil {
				usage = cd.usage
				if err := emitDone(); err != nil {
					return err
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("llama: read stream: %w", readErr)
		}
	}
	if !emittedDone {
		return emitDone()
	}
	return nil
}

type chunkData struct {
	content   string
	reasoning string
	toolFrags []toolFrag
	usage     *Usage
}

type toolFrag struct {
	index int
	id    string
	name  string
	args  string
}

func parseChatChunk(data string) (chunkData, error) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return chunkData{}, err
	}
	var cd chunkData
	if len(chunk.Choices) > 0 {
		c := chunk.Choices[0]
		cd.content = c.Delta.Content
		cd.reasoning = c.Delta.ReasoningContent
		for _, tc := range c.Delta.ToolCalls {
			cd.toolFrags = append(cd.toolFrags, toolFrag{
				index: tc.Index,
				id:    tc.ID,
				name:  tc.Function.Name,
				args:  tc.Function.Arguments,
			})
		}
	}
	if chunk.Usage != nil {
		cd.usage = &Usage{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
		}
	}
	return cd, nil
}

func toolsWire(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name, "description": t.Description}
		if len(t.Parameters) > 0 {
			fn["parameters"] = t.Parameters
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func (l *LlamaCppNative) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, l.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if l.token != "" {
		req.Header.Set("Authorization", "Bearer "+l.token)
	}
	return req, nil
}

func (l *LlamaCppNative) post(ctx context.Context, path string, body any) (*http.Response, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := l.newRequest(ctx, http.MethodPost, path, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	return l.do(req)
}

func (l *LlamaCppNative) getJSON(ctx context.Context, path string, out any) error {
	req, err := l.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	resp, err := l.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeJSON(resp, path, out)
}

func (l *LlamaCppNative) postJSON(ctx context.Context, path string, body, out any) error {
	resp, err := l.post(ctx, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeJSON(resp, path, out)
}

func decodeJSON(resp *http.Response, path string, out any) error {
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("llama: decode %s: %w", path, err)
	}
	return nil
}

func (l *LlamaCppNative) do(req *http.Request) (*http.Response, error) {
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llama: %s %s: %w", req.Method, req.URL.Path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("llama: %s %s: status %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp, nil
}
