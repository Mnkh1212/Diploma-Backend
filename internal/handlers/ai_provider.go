package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// AI provider — Pollinations.ai ашиглана. API key шаардахгүй, OpenAI-compatible
// endpoint-той учраас messages array-ыг direct-аар POST хийдэг.
//
// Хариу нь choices[0].message.content (стандарт OpenAI шаблон).

const pollinationsURL = "https://text.pollinations.ai/openai"

// pollinationsModel — анхдагч модель. "openai" нь GPT-OSS-20B руу route-длэдэг,
// харин Mongolian тал илүү жигд хариу өгдөг учраас энэ загварыг сонгосон.
const pollinationsModel = "openai"

type aiMessage struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

type pollinationsRequest struct {
	Model    string      `json:"model"`
	Messages []aiMessage `json:"messages"`
}

type pollinationsResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// callAI — system prompt + chat history-тай хүсэлт илгээж AI-ийн хариу буцаана.
// historyMessages нь "user" / "assistant" роль бүхий мөрнүүд (хуучин харилцаа).
// Алдаа гарвал error буцаана; caller fallback ажиллуулна.
func callAI(ctx context.Context, systemPrompt string, historyMessages []aiMessage, userMessage string) (string, error) {
	msgs := []aiMessage{{Role: "system", Content: systemPrompt}}
	msgs = append(msgs, historyMessages...)
	if userMessage != "" {
		msgs = append(msgs, aiMessage{Role: "user", Content: userMessage})
	}

	body, err := json.Marshal(pollinationsRequest{Model: pollinationsModel, Messages: msgs})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pollinationsURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pollinations status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed pollinationsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("pollinations response parse: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("pollinations returned no choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("pollinations empty content")
	}
	return content, nil
}

// stripCodeFence — AI заримдаа JSON-аа ```json ... ``` дотор буцаадаг.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
