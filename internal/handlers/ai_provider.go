package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"fintrack-backend/internal/config"
)

// sanitizeUTF8 - PDF parser-аас орж ирсэн invalid byte sequences-ыг арилгана.
// Gemini protobuf нь invalid UTF-8 текстэд "Part.text contains invalid UTF-8"
// гэсэн алдаа өгдөг. OpenRouter JSON serialization бас зохицуулна.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}

// ===================== OpenRouter (Gemini geo-block-аас гарах fallback) =====================
//
// OpenRouter (https://openrouter.ai) нь олон LLM-ийг OpenAI-compatible API-аар
// дамжуулдаг proxy. Монголоос Google Gemini API-руу шууд хандах боломжгүй
// (User location is not supported) тохиолдолд OpenRouter-ийг ашиглаж тойрно.
//
// API key үнэгүй авна — https://openrouter.ai/keys → Render env-д
// `OPENROUTER_API_KEY` гэж тавиад л болсон.

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orRequest struct {
	Model    string      `json:"model"`
	Messages []orMessage `json:"messages"`
}

type orChoice struct {
	Message orMessage `json:"message"`
}

type orResponse struct {
	Choices []orChoice `json:"choices"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callOpenRouter - системийн prompt + чатын түүх + хэрэглэгчийн мессеж бэлтгэн
// OpenRouter-ийн chat completions endpoint руу илгээж текст хариу авна.
//
// systemPrompt: Gemini-ийн SystemInstruction-той дүйцнэ
// history: өмнөх ярианы мессежүүд (user/assistant дараалсан)
// userMessage: одоогийн хэрэглэгчийн ярианы мөр
func callOpenRouter(ctx context.Context, cfg *config.Config, systemPrompt string, history []orMessage, userMessage string) (string, error) {
	if cfg.OpenRouterAPIKey == "" {
		return "", errors.New("OPENROUTER_API_KEY not configured")
	}

	messages := make([]orMessage, 0, len(history)+2)
	if systemPrompt != "" {
		messages = append(messages, orMessage{Role: "system", Content: sanitizeUTF8(systemPrompt)})
	}
	for _, h := range history {
		messages = append(messages, orMessage{Role: h.Role, Content: sanitizeUTF8(h.Content)})
	}
	messages = append(messages, orMessage{Role: "user", Content: sanitizeUTF8(userMessage)})

	body, err := json.Marshal(orRequest{
		Model:    cfg.OpenRouterModel,
		Messages: messages,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.OpenRouterAPIKey)
	req.Header.Set("HTTP-Referer", "https://fintrack-api-lgei.onrender.com")
	req.Header.Set("X-Title", "FinTrack")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed orResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("openrouter parse: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("openrouter: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("openrouter empty response")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("openrouter empty content")
	}
	return content, nil
}

// isGeoBlockError - Gemini-ийн "User location is not supported" гэх мэт
// бүс нутгийн хязгаарлалтаас үүдсэн алдаа эсэхийг шалгана.
func isGeoBlockError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "user location is not supported") ||
		strings.Contains(msg, "location is not supported") ||
		strings.Contains(msg, "not available in your country") ||
		strings.Contains(msg, "country not supported")
}

// shouldTryFallback - Gemini-ийн fail-аар OpenRouter руу хандах эсэхийг шийднэ.
// Geo-block, quota, rate limit, model not found — fallback хийе.
// API key буруу гэх мэт fundamental алдаанд бол хийхгүй.
func shouldTryFallback(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if isGeoBlockError(err) {
		return true
	}
	return strings.Contains(msg, "quota") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "model") && strings.Contains(msg, "not found") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "unavailable")
}

// formatMultiProviderError - OpenRouter + Gemini хоёулаа fail хийсэн үед
// хэрэглэгчид яг ямар асуудал байгааг товч, шалгах боломжтой жагсаалттай харуулна.
func formatMultiProviderError(geminiErr, orErr error) string {
	var b strings.Builder
	b.WriteString("❌ AI үйлчилгээ ажиллахгүй байна.\n\n")

	if orErr != nil {
		b.WriteString(fmt.Sprintf("• OpenRouter: %s\n", shortErr(orErr)))
	}
	if geminiErr != nil {
		if isGeoBlockError(geminiErr) {
			b.WriteString("• Gemini: Монголоос хандах боломжгүй (geo-block)\n")
		} else {
			b.WriteString(fmt.Sprintf("• Gemini: %s\n", shortErr(geminiErr)))
		}
	}

	b.WriteString("\nШалгах зүйлс:\n")
	if orErr != nil {
		orMsg := strings.ToLower(orErr.Error())
		switch {
		case strings.Contains(orMsg, "401"), strings.Contains(orMsg, "unauthorized"):
			b.WriteString("• OPENROUTER_API_KEY буруу. https://openrouter.ai/keys → шинэ key үүсгэх\n")
		case strings.Contains(orMsg, "404"), strings.Contains(orMsg, "model"), strings.Contains(orMsg, "not found"):
			b.WriteString("• OPENROUTER_MODEL буруу. Render env-д `google/gemini-2.0-flash-exp:free` гэж тохируулах\n")
		case strings.Contains(orMsg, "402"), strings.Contains(orMsg, "credit"), strings.Contains(orMsg, "balance"), strings.Contains(orMsg, "payment"):
			b.WriteString("• OpenRouter дансанд credit нэмэх эсвэл `:free` модел сонгох\n")
		case strings.Contains(orMsg, "429"), strings.Contains(orMsg, "rate"):
			b.WriteString("• OpenRouter rate limit — хэдэн минут хүлээгээд дахин оролдоорой\n")
		case strings.Contains(orMsg, "verify"), strings.Contains(orMsg, "verification"):
			b.WriteString("• OpenRouter free models-д заримдаа phone verification шаарддаг — https://openrouter.ai/settings/privacy\n")
		default:
			b.WriteString("• OpenRouter dashboard (https://openrouter.ai/activity) дээр сүүлийн request-ээ шалгах\n")
		}
	} else {
		b.WriteString("• Render → Environment → `OPENROUTER_API_KEY` нэмэх (https://openrouter.ai/keys)\n")
	}
	if geminiErr != nil && isGeoBlockError(geminiErr) {
		b.WriteString("• Gemini-г Монголоос ашиглах боломжгүй учир OpenRouter л шийдэл\n")
	}

	return b.String()
}

func shortErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return msg
}

func init() {
	// Background-д лог гаргахын тулд log хэрэгтэй.
	_ = log.Println
}
