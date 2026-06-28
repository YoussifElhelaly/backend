package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 60 * time.Second}

const variantPrompt = `You are a WhatsApp marketing message specialist helping avoid spam detection.

Generate exactly 5 alternative versions of the message below. Each version must:
- Convey exactly the same meaning and call-to-action
- Use different wording, sentence structure, or tone
- Sound natural and human-written (no robotic phrasing)
- Not be identical to each other or to the original
- Keep approximately the same length as the original
- Preserve any {name} placeholders exactly as-is

Original message:
%s

Respond with ONLY a valid JSON array of exactly 5 strings. No explanation, no markdown, no extra text:
["variant1","variant2","variant3","variant4","variant5"]`

// GenerateVariants calls the appropriate AI provider and returns 5 message variants.
func GenerateVariants(platform, model, apiKey, message string) ([]string, error) {
	prompt := fmt.Sprintf(variantPrompt, message)
	switch platform {
	case "openai":
		return callOpenAI(apiKey, model, prompt)
	case "anthropic":
		return callAnthropic(apiKey, model, prompt)
	case "gemini":
		return callGemini(apiKey, model, prompt)
	default:
		return nil, fmt.Errorf("unknown platform: %s", platform)
	}
}

// TestConnection verifies the API key works by sending a minimal request.
func TestConnection(platform, model, apiKey string) error {
	_, err := GenerateVariants(platform, model, apiKey, "Hello!")
	return err
}

// ── OpenAI ───────────────────────────────────────────────────────────────────

func callOpenAI(apiKey, model, prompt string) ([]string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.9,
	})
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("openai error %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var res struct {
		Choices []struct {
			Message struct{ Content string `json:"content"` } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Choices) == 0 {
		return nil, fmt.Errorf("unexpected openai response")
	}
	return parseVariants(res.Choices[0].Message.Content)
}

// ── Anthropic ────────────────────────────────────────────────────────────────

func callAnthropic(apiKey, model, prompt string) ([]string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("anthropic error %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var res struct {
		Content []struct {
			Text string `json:"text"`
			Type string `json:"type"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Content) == 0 {
		return nil, fmt.Errorf("unexpected anthropic response")
	}
	return parseVariants(res.Content[0].Text)
}

// ── Gemini ───────────────────────────────────────────────────────────────────

func callGemini(apiKey, model, prompt string) ([]string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
	body, _ := json.Marshal(map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]string{{"text": prompt}}},
		},
	})
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("gemini error %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct{ Text string `json:"text"` } `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || len(res.Candidates) == 0 {
		return nil, fmt.Errorf("unexpected gemini response")
	}
	return parseVariants(res.Candidates[0].Content.Parts[0].Text)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func parseVariants(text string) ([]string, error) {
	// Strip markdown code fences if present
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "["); idx >= 0 {
		text = text[idx:]
	}
	if idx := strings.LastIndex(text, "]"); idx >= 0 {
		text = text[:idx+1]
	}

	var variants []string
	if err := json.Unmarshal([]byte(text), &variants); err != nil {
		return nil, fmt.Errorf("failed to parse AI response as JSON array: %w", err)
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("AI returned empty variants list")
	}
	return variants, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
