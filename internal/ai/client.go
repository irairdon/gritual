package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	HTTP     *http.Client
	ChatHTTP *http.Client
	APIKey   string
	BaseURL  string
}

func NewClient(apiKey string, httpClient *http.Client) *Client {
	vision := httpClient
	chat := httpClient
	if httpClient == nil {
		vision = &http.Client{Timeout: 25 * time.Second}
		chat = &http.Client{Timeout: 55 * time.Second}
	}
	return &Client{
		HTTP:     vision,
		ChatHTTP: chat,
		APIKey:   apiKey,
		BaseURL:  DefaultBaseURL,
	}
}

type completionsReq struct {
	Model          string          `json:"model"`
	User           string          `json:"user"`
	MaxTokens      int             `json:"max_tokens"`
	Messages       []message       `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type responseFormat struct {
	Type       string         `json:"type"`
	JSONSchema jsonSchemaWrap `json:"json_schema"`
}

type jsonSchemaWrap struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type completionsResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *Client) CompleteJSON(ctx context.Context, req JSONRequest) (json.RawMessage, error) {
	if c == nil || c.APIKey == "" {
		return nil, ErrUnavailable
	}
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = VisionMaxTokens
	}
	schemaName := req.SchemaName
	if schemaName == "" {
		schemaName = SchemaName
	}
	schema := req.Schema
	if len(schema) == 0 {
		schema = mealSchema
	}

	userContent := []map[string]any{}
	if len(req.ImageJPEG) > 0 {
		userContent = append(userContent, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url":    "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(req.ImageJPEG),
				"detail": "high",
			},
		})
	}
	userContent = append(userContent, map[string]any{
		"type": "text",
		"text": req.Text,
	})

	body := completionsReq{
		Model:     model,
		User:      req.User,
		MaxTokens: maxTok,
		Messages: []message{
			{Role: "system", Content: req.System},
			{Role: "user", Content: userContent},
		},
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaWrap{
				Name:   schemaName,
				Strict: true,
				Schema: schema,
			},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	base := strings.TrimRight(c.BaseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+CompletionsPath, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 400 {
		slog.Error("xai completions", "status", res.StatusCode)
		return nil, ErrUnavailable
	}
	var parsed completionsResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, ErrUnavailable
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return nil, ErrUnavailable
	}
	slog.Info("xai vision", "tokens_in", parsed.Usage.PromptTokens, "tokens_out", parsed.Usage.CompletionTokens)
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if !json.Valid([]byte(content)) {
		return nil, ErrUnavailable
	}
	return json.RawMessage(content), nil
}
