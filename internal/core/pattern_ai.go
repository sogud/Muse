package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PatternInput struct {
	Idea  string `json:"idea"`
	Style string `json:"style"`
	BPM   int    `json:"bpm"`
	Bars  int    `json:"bars"`
}

type PatternOutput struct {
	Title string `json:"title"`
	Code  string `json:"code"`
	Notes string `json:"notes"`
}

// AiPattern writes editable oto DSL using the configured model or local gateway.
type AiPattern struct{ settings *NativeSettings }

func NewAiPattern(settings *NativeSettings) *AiPattern {
	return &AiPattern{settings: settings}
}

const patternSystemPrompt = `你是 oto 的音乐代码生成器。
当前产品阶段只生成无人声、无歌词、无演唱的电子音乐草稿。
你只能生成 oto pattern DSL，不要生成 JavaScript，不要生成 Strudel 代码。
不要写 lyrics、vocal、singer、verse、chorus 等歌词或人声内容。
优先电子音乐结构：drums、bass、lead、chords/pad，适合循环、试听和后续转 MIDI/WAV。
风格可以偏 synthwave、deep house、ambient techno、future bass、lo-fi electronic、drum and bass、IDM。
鼓组 pattern 每个 token 必须是 4 步，只能由 x 和 . 组成，例如 x...、..x.、....；不要输出 x.. 这种 3 步 token。
DSL 语法：
tempo <60-160>
scale <Root> <major|minor>

drums:
  kick  x... x...
  snare ..x. ..x.
  hat   x x x x

bass:
  notes A2 A2 C3 E3
  rhythm 1/2 1/2 1/2 1/2
  sound warm_bass

lead:
  notes A4 C5 E5 G5
  rhythm 1/4 1/4 1/2 1
  sound soft_sine

chords:
  notes A3 C4 E4 | F3 A3 C4 | G3 B3 D4 | E3 G3 B3
  rhythm 1 1 1 1
  sound pad

只输出 JSON：{ "title": string, "code": string, "notes": string }。
code 必须能被上述 DSL 解析；不要使用未说明的语法。`

func validatedAIURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("invalid AI endpoint URL")
	}
	if parsed.Scheme == "https" {
		return strings.TrimRight(parsed.String(), "/"), nil
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return strings.TrimRight(parsed.String(), "/"), nil
	}
	return "", errors.New("AI endpoint must use HTTPS, or HTTP on localhost")
}

func patternJSON(text string) (PatternOutput, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 3 {
			text = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	var value PatternOutput
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return value, errors.New("model did not return a valid JSON pattern")
	}
	if strings.TrimSpace(value.Title) == "" || strings.TrimSpace(value.Code) == "" || strings.TrimSpace(value.Notes) == "" {
		return value, errors.New("AI pattern is missing title, code, or notes")
	}
	value.Code = normalizePattern(value.Code)
	return value, nil
}

var drumRow = regexp.MustCompile(`^(\s*)(kick|snare|hat|clap|perc)\s+([xX.\s]+)$`)
var rhythmLine = regexp.MustCompile(`(?i)^\s*rhythm\s+`)

func normalizePattern(code string) string {
	lines := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	inDrums := false
	for i, line := range lines {
		line = strings.TrimRight(line, " \t")
		section := strings.ToLower(strings.TrimSpace(line))
		if strings.HasSuffix(section, ":") {
			inDrums = section == "drums:"
		}
		if rhythmLine.MatchString(line) {
			line = strings.ReplaceAll(line, "|", "")
		}
		if inDrums {
			pieces := drumRow.FindStringSubmatch(line)
			if len(pieces) == 4 {
				pattern := strings.ToLower(strings.ReplaceAll(pieces[3], " ", ""))
				pattern = strings.ReplaceAll(pattern, "\t", "")
				for len(pattern)%4 != 0 {
					pattern += "."
				}
				var chunks []string
				for j := 0; j < len(pattern); j += 4 {
					chunks = append(chunks, pattern[j:j+4])
				}
				line = pieces[1] + strings.ToLower(pieces[2]) + "  " + strings.Join(chunks, " ")
			}
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func (a *AiPattern) Generate(ctx context.Context, input PatternInput) (PatternOutput, error) {
	if len(strings.TrimSpace(input.Idea)) == 0 || len(input.Idea) > 1000 || len(input.Style) > 200 {
		return PatternOutput{}, errors.New("please provide a music idea (up to 1000 characters)")
	}
	if input.BPM != 0 && (input.BPM < 60 || input.BPM > 160) {
		return PatternOutput{}, errors.New("BPM must be between 60 and 160")
	}
	if input.Bars != 0 && (input.Bars < 1 || input.Bars > 32) {
		return PatternOutput{}, errors.New("bars must be between 1 and 32")
	}
	inputData, _ := json.Marshal(input)
	prompt := "根据下面的输入生成 oto pattern。只输出 JSON，不要 Markdown、代码围栏或额外解释。输入：\n" + string(inputData)
	content, err := a.generateText(ctx, patternSystemPrompt, prompt)
	if err != nil {
		return PatternOutput{}, err
	}
	return patternJSON(content)
}

func (a *AiPattern) TestAi(ctx context.Context) AIStatus {
	text, err := a.generateText(ctx, "Return only one short plain text response.", "Reply with ready.")
	if err != nil {
		return AIStatus{OK: false, Message: "AI 连接失败：" + err.Error()}
	}
	if strings.TrimSpace(text) == "" {
		return AIStatus{OK: false, Message: "AI returned empty text"}
	}
	return AIStatus{OK: true, Message: "AI 连接正常"}
}

func (a *AiPattern) TestPi(ctx context.Context) AIStatus {
	return a.TestAi(ctx)
}

func (a *AiPattern) generateText(ctx context.Context, system, prompt string) (string, error) {
	cfg := a.settings.aiConfiguration()
	provider := cfg.Provider
	if provider == "" {
		provider = "openai"
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-4o-mini"
	}
	if cfg.APIKey == "" && provider != "local" {
		return "", errors.New("AI API key is not configured")
	}
	model := cfg.Model
	base := cfg.BaseURL
	switch provider {
	case "openai", "deepseek", "local":
		if base == "" {
			switch provider {
			case "openai":
				base = "https://api.openai.com/v1"
			case "deepseek":
				base = "https://api.deepseek.com/v1"
			case "local":
				base = "http://localhost:1234/v1"
			}
		}
		valid, err := validatedAIURL(base)
		if err != nil {
			return "", err
		}
		endpoint := valid
		if !strings.HasSuffix(endpoint, "/chat/completions") {
			endpoint += "/chat/completions"
		}
		payload := map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "system", "content": system},
				{"role": "user", "content": prompt},
			},
			"temperature": 0.4,
		}
		var response struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := aiJSONRequest(ctx, http.MethodPost, endpoint, cfg.APIKey, payload, &response); err != nil {
			return "", err
		}
		if len(response.Choices) == 0 {
			return "", errors.New("AI provider returned no choices")
		}
		return response.Choices[0].Message.Content, nil
	case "google":
		if base == "" {
			base = "https://generativelanguage.googleapis.com/v1beta"
		}
		valid, err := validatedAIURL(base)
		if err != nil {
			return "", err
		}
		endpoint := valid + "/models/" + url.PathEscape(model) + ":generateContent"
		payload := map[string]any{
			"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
			"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": prompt}}}},
		}
		text, err := postProviderText(ctx, endpoint, cfg.APIKey, payload, "google")
		return text, err
	case "anthropic":
		if base == "" {
			base = "https://api.anthropic.com/v1"
		}
		valid, err := validatedAIURL(base)
		if err != nil {
			return "", err
		}
		endpoint := valid + "/messages"
		payload := map[string]any{
			"model": model, "max_tokens": 2048, "system": system,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		}
		return postProviderText(ctx, endpoint, cfg.APIKey, payload, "anthropic")
	default:
		return "", fmt.Errorf("unsupported AI provider %q", provider)
	}
}

func postProviderText(ctx context.Context, endpoint, key string, payload any, provider string) (string, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if provider == "google" {
		req.Header.Set("x-goog-api-key", key)
	} else {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	client := http.Client{Timeout: 45 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("%s API returned HTTP %s", provider, strconv.Itoa(res.StatusCode))
	}
	if provider == "google" {
		var response struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&response); err != nil {
			return "", err
		}
		if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
			return "", errors.New("Gemini returned empty content")
		}
		return response.Candidates[0].Content.Parts[0].Text, nil
	}
	var response struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&response); err != nil {
		return "", err
	}
	if len(response.Content) == 0 {
		return "", errors.New("Anthropic returned empty content")
	}
	return response.Content[0].Text, nil
}
