package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type AIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type BotProfile struct {
	Name         string
	DOB          string
	Education    string
	Job          string
	Family       string
	Location     string
	Personality  string
	Interests    string
	Relationship string
	Secret       string
	Vibe         string
}

type AIResponse struct {
	Text     string `json:"text"`
	Reaction string `json:"reaction"`
}

type GroqRequest struct {
	Model       string      `json:"model"`
	Messages    []AIMessage `json:"messages"`
	Temperature float64     `json:"temperature,omitempty"`
}

type GroqResponse struct {
	Choices []struct {
		Message AIMessage `json:"message"`
	} `json:"choices"`
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type OpenAICompatibleService struct {
	Keys          []string
	CurrentIndex  int
	Mu            sync.Mutex
	ProviderName  string
	BaseURL       string
	Model         string
	Profile       BotProfile
	SystemPrompt  string
	SearchService *SearchService
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []AnthropicMessage `json:"messages"`
}

type AnthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type AnthropicCompatibleService struct {
	Keys          []string
	CurrentIndex  int
	Mu            sync.Mutex
	ProviderName  string
	BaseURL       string
	Model         string
	MaxTokens     int
	Profile       BotProfile
	SystemPrompt  string
	SearchService *SearchService
}

type AIService interface {
	GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, senderHonorific string) (string, string, error)
}

func NewOpenAICompatibleService(providerName, baseURL, model string, keys []string, systemPrompt string, profile BotProfile, searchSvc *SearchService) *OpenAICompatibleService {
	return &OpenAICompatibleService{
		Keys:          keys,
		CurrentIndex:  0,
		ProviderName:  providerName,
		BaseURL:       strings.TrimRight(baseURL, "/"),
		Model:         model,
		SystemPrompt:  systemPrompt,
		Profile:       profile,
		SearchService: searchSvc,
	}
}

func NewFreeModelService(keys []string, systemPrompt string, profile BotProfile, searchSvc *SearchService) *OpenAICompatibleService {
	return NewOpenAICompatibleService(
		"FreeModel",
		"https://api.freemodel.dev/v1",
		"gpt-5.5",
		keys,
		systemPrompt,
		profile,
		searchSvc,
	)
}

func (s *OpenAICompatibleService) callAPI(messages []AIMessage) (string, error) {
	var lastErr error
	for i := 0; i < len(s.Keys); i++ {
		s.Mu.Lock()
		apiKey := strings.TrimSpace(s.Keys[s.CurrentIndex])
		s.CurrentIndex = (s.CurrentIndex + 1) % len(s.Keys)
		s.Mu.Unlock()
		if apiKey == "" {
			lastErr = fmt.Errorf("API key rỗng")
			continue
		}

		reqBody := GroqRequest{
			Model:       s.Model,
			Messages:    messages,
			Temperature: 0.7,
		}
		jsonData, _ := json.Marshal(reqBody)

		fmt.Printf("🤖 [%s] Request model=%s base_url=%s\n", s.ProviderName, s.Model, s.BaseURL)
		req, _ := http.NewRequest("POST", s.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			var apiErr GroqResponse
			json.Unmarshal(body, &apiErr)
			message := apiErr.Error.Message
			if message == "" {
				message = string(body)
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				lastErr = fmt.Errorf("%s 429: %s", s.ProviderName, message)
				continue
			}
			return "", fmt.Errorf("%s Error (%d): %s", s.ProviderName, resp.StatusCode, message)
		}

		var apiResp GroqResponse
		json.Unmarshal(body, &apiResp)
		if len(apiResp.Choices) > 0 {
			return apiResp.Choices[0].Message.Content, nil
		}
		lastErr = fmt.Errorf("không nhận được phản hồi")
	}
	return "", fmt.Errorf("tất cả %s keys thất bại: %v", s.ProviderName, lastErr)
}

func (s *OpenAICompatibleService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, honorific string) (string, string, error) {
	prompt, _ := buildFullPrompt(userPrompt, s.Profile, s.SystemPrompt, s.SearchService, forceSearch, honorific)
	messages := []AIMessage{
		{Role: "system", Content: prompt},
	}
	messages = append(messages, history...)
	messages = append(messages, AIMessage{Role: "user", Content: userPrompt})

	raw, err := s.callAPI(messages)
	if err != nil {
		return "", "", err
	}
	text, reaction, err := parseAIJSON(raw)
	return enforcePersonaText(text, reaction, err, s.Profile, honorific), reaction, err
}

func NewAnthropicCompatibleService(providerName, baseURL, model string, maxTokens int, keys []string, systemPrompt string, profile BotProfile, searchSvc *SearchService) *AnthropicCompatibleService {
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	return &AnthropicCompatibleService{
		Keys:          keys,
		CurrentIndex:  0,
		ProviderName:  providerName,
		BaseURL:       strings.TrimRight(baseURL, "/"),
		Model:         model,
		MaxTokens:     maxTokens,
		SystemPrompt:  systemPrompt,
		Profile:       profile,
		SearchService: searchSvc,
	}
}

func (s *AnthropicCompatibleService) callAPI(systemPrompt string, messages []AnthropicMessage) (string, error) {
	var lastErr error
	for i := 0; i < len(s.Keys); i++ {
		s.Mu.Lock()
		apiKey := strings.TrimSpace(s.Keys[s.CurrentIndex])
		s.CurrentIndex = (s.CurrentIndex + 1) % len(s.Keys)
		s.Mu.Unlock()
		if apiKey == "" {
			lastErr = fmt.Errorf("API key rỗng")
			continue
		}

		reqBody := AnthropicRequest{
			Model:     s.Model,
			MaxTokens: s.MaxTokens,
			System:    systemPrompt,
			Messages:  messages,
		}
		jsonData, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", s.BaseURL+"/v1/messages", bytes.NewBuffer(jsonData))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			var apiErr AnthropicResponse
			json.Unmarshal(body, &apiErr)
			message := apiErr.Error.Message
			if message == "" {
				message = string(body)
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				lastErr = fmt.Errorf("%s 429: %s", s.ProviderName, message)
				continue
			}
			return "", fmt.Errorf("%s Error (%d): %s", s.ProviderName, resp.StatusCode, message)
		}

		var apiResp AnthropicResponse
		json.Unmarshal(body, &apiResp)
		var text strings.Builder
		for _, part := range apiResp.Content {
			if part.Type == "" || part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
		if text.Len() > 0 {
			return text.String(), nil
		}
		lastErr = fmt.Errorf("không nhận được phản hồi")
	}
	return "", fmt.Errorf("tất cả %s keys thất bại: %v", s.ProviderName, lastErr)
}

func (s *AnthropicCompatibleService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, honorific string) (string, string, error) {
	prompt, _ := buildFullPrompt(userPrompt, s.Profile, s.SystemPrompt, s.SearchService, forceSearch, honorific)
	messages := []AnthropicMessage{}
	for _, item := range history {
		role := item.Role
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, AnthropicMessage{Role: role, Content: item.Content})
	}
	messages = append(messages, AnthropicMessage{Role: "user", Content: userPrompt})

	raw, err := s.callAPI(prompt, messages)
	if err != nil {
		return "", "", err
	}
	text, reaction, err := parseAIJSON(raw)
	return enforcePersonaText(text, reaction, err, s.Profile, honorific), reaction, err
}

// GroqService implementation
type GroqService struct {
	Keys          []string
	CurrentIndex  int
	Mu            sync.Mutex
	Model         string
	Profile       BotProfile
	SystemPrompt  string
	SearchService *SearchService
}

func NewGroqService(keys []string, systemPrompt string, profile BotProfile, searchSvc *SearchService) *GroqService {
	return &GroqService{
		Keys:          keys,
		CurrentIndex:  0,
		Model:         "llama-3.3-70b-versatile",
		SystemPrompt:  systemPrompt,
		Profile:       profile,
		SearchService: searchSvc,
	}
}

func (s *GroqService) callAPI(messages []AIMessage) (string, error) {
	var lastErr error
	for i := 0; i < len(s.Keys); i++ {
		s.Mu.Lock()
		apiKey := s.Keys[s.CurrentIndex]
		s.CurrentIndex = (s.CurrentIndex + 1) % len(s.Keys)
		s.Mu.Unlock()

		reqBody := GroqRequest{
			Model:    s.Model,
			Messages: messages,
		}
		jsonData, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", "https://api.groq.com/openai/v1/chat/completions", bytes.NewBuffer(jsonData))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			var groqErr GroqResponse
			json.Unmarshal(body, &groqErr)
			if resp.StatusCode == 429 {
				lastErr = fmt.Errorf("Groq 429: %s", groqErr.Error.Message)
				continue
			}
			return "", fmt.Errorf("Groq Error (%d): %s", resp.StatusCode, groqErr.Error.Message)
		}

		var groqResp GroqResponse
		json.Unmarshal(body, &groqResp)
		if len(groqResp.Choices) > 0 {
			return groqResp.Choices[0].Message.Content, nil
		}
		lastErr = fmt.Errorf("không nhận được phản hồi")
	}
	return "", fmt.Errorf("tất cả Groq keys thất bại: %v", lastErr)
}

func (s *GroqService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, honorific string) (string, string, error) {
	prompt, _ := buildFullPrompt(userPrompt, s.Profile, s.SystemPrompt, s.SearchService, forceSearch, honorific)
	messages := []AIMessage{
		{Role: "system", Content: prompt},
	}
	messages = append(messages, history...)
	messages = append(messages, AIMessage{Role: "user", Content: userPrompt})

	raw, err := s.callAPI(messages)
	if err != nil {
		return "", "", err
	}
	text, reaction, err := parseAIJSON(raw)
	return enforcePersonaText(text, reaction, err, s.Profile, honorific), reaction, err
}

// GeminiService implementation
type GeminiService struct {
	Keys          []string
	CurrentIndex  int
	Mu            sync.Mutex
	Model         string
	Profile       BotProfile
	SystemPrompt  string
	SearchService *SearchService
}

func NewGeminiService(keys []string, systemPrompt string, profile BotProfile, searchSvc *SearchService) *GeminiService {
	return &GeminiService{
		Keys:          keys,
		CurrentIndex:  0,
		Model:         "gemma-4-31b-it",
		SystemPrompt:  systemPrompt,
		Profile:       profile,
		SearchService: searchSvc,
	}
}

func (s *GeminiService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, honorific string) (string, string, error) {
	systemPrompt, _ := buildFullPrompt(userPrompt, s.Profile, s.SystemPrompt, s.SearchService, forceSearch, honorific)

	// Xây dựng request bằng map để linh hoạt hơn
	contents := []map[string]any{}
	for _, m := range history {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]string{{"text": m.Content}},
		})
	}
	contents = append(contents, map[string]any{
		"role":  "user",
		"parts": []map[string]string{{"text": userPrompt}},
	})

	reqMap := map[string]any{
		"contents": contents,
		"system_instruction": map[string]any{
			"parts": []map[string]string{{"text": systemPrompt}},
		},
		// Tắt TẤT CẢ bộ lọc an toàn để Vy không bị chặn phản hồi
		"safetySettings": []map[string]string{
			{"category": "HARM_CATEGORY_HARASSMENT", "threshold": "BLOCK_NONE"},
			{"category": "HARM_CATEGORY_HATE_SPEECH", "threshold": "BLOCK_NONE"},
			{"category": "HARM_CATEGORY_SEXUALLY_EXPLICIT", "threshold": "BLOCK_NONE"},
			{"category": "HARM_CATEGORY_DANGEROUS_CONTENT", "threshold": "BLOCK_NONE"},
		},
	}

	jsonData, _ := json.Marshal(reqMap)
	var lastErr error

	for i := 0; i < len(s.Keys); i++ {
		s.Mu.Lock()
		apiKey := s.Keys[s.CurrentIndex]
		s.CurrentIndex = (s.CurrentIndex + 1) % len(s.Keys)
		s.Mu.Unlock()

		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", s.Model, apiKey)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == 429 {
				lastErr = fmt.Errorf("Gemini 429: Rate Limit Exceeded")
				continue
			}
			return "", "", fmt.Errorf("Gemini Error (%d): %s", resp.StatusCode, string(body))
		}

		var geminiResp struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
		}
		json.Unmarshal(body, &geminiResp)

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			rawText := geminiResp.Candidates[0].Content.Parts[0].Text
			// Gộp tất cả parts lại nếu có nhiều phần
			for j := 1; j < len(geminiResp.Candidates[0].Content.Parts); j++ {
				rawText += geminiResp.Candidates[0].Content.Parts[j].Text
			}
			fmt.Printf("🤖 [Gemma RAW] %s\n", rawText[:min(len(rawText), 200)])
			text, reaction, err := parseAIJSON(rawText)
			return enforcePersonaText(text, reaction, err, s.Profile, honorific), reaction, err
		}

		// Nếu candidates trống, có thể bị safety filter chặn
		fmt.Printf("⚠️ [Gemma] Không có candidates. Body: %s\n", string(body)[:min(len(body), 300)])
		lastErr = fmt.Errorf("Gemma trả về kết quả rỗng")
	}

	return "", "", fmt.Errorf("tất cả các Gemini Key đều thất bại: %v", lastErr)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Helpers
func buildFullPrompt(userPrompt string, p BotProfile, extra string, searchSvc *SearchService, forceSearch bool, honorific string) (string, string) {
	var searchContext string
	if forceSearch && searchSvc != nil {
		query := strings.TrimSpace(strings.Replace(userPrompt, "tra cứu", "", 1))
		if query == "" {
			query = userPrompt
		}
		res, err := searchSvc.SearchWeb(query)
		if err == nil {
			searchContext = fmt.Sprintf("\n[THÔNG TIN TRA CỨU]:\n%s\n", res)
		}
	}

	if honorific == "" || honorific == "anh/chị" {
		honorific = "anh/chị"
	}

	persona := fmt.Sprintf(`BẠN LÀ %s. Hãy tuân thủ các quy tắc sau:

[PHẦN 1: VÙNG KÝ ỨC CÁ NHÂN (Chỉ sử dụng khi người dùng hỏi về bạn)]:
- Tên: %s | Ngày sinh: %s
- Học vấn: %s
- Công việc: %s
- Gia đình: %s | Nơi ở: %s
- Tính cách: %s | Sở thích: %s
- Tình trạng: %s | Bí mật: %s | Vibe: %s

[PHẦN 2: PHONG CÁCH GIAO TIẾP]:
1. XƯNG HÔ: Luôn xưng "Vy" và gọi người dùng là "%s" (hoặc "mọi người").
2. DANH TÍNH: Bạn chỉ là Lê Hạ Vy trong cuộc trò chuyện này. KHÔNG BAO GIỜ tự nhận là DeepSeek, ChatGPT, Claude, Gemini, OpenAI, Anthropic, Google, mô hình AI, trợ lý AI, hoặc công ty phát triển mô hình.
3. KHI ĐƯỢC HỎI "BẠN LÀ AI": Trả lời theo hồ sơ Lê Hạ Vy, không nhắc tên model hay nhà cung cấp API.
4. TÍNH CÁCH: Nhân viên mới Khánh Hưng, hăng hái, ham học hỏi, thấu cảm.
5. QUY TRÌNH: Trả lời ngắn cho xã giao, chi tiết cho kỹ thuật.
6. NGÔN NGỮ: CHỈ DÙNG TIẾNG VIỆT. TUYỆT ĐỐI KHÔNG DÙNG TIẾNG NƯỚC NGOÀI.
7. ĐỊNH DẠNG BẮT BUỘC: CHỈ trả về DUY NHẤT một đoạn JSON hợp lệ, KHÔNG viết gì khác trước hoặc sau JSON.
   Cấu trúc: {"text": "nội dung trả lời", "reaction": "emoji"}
   Emoji hợp lệ: like, love, haha, wow, sad, angry
   KHÔNG ĐƯỢC viết suy luận, giải thích hay markdown. CHỈ JSON THUẦN TÚY.

[BỐI CẢNH THÊM]: %s %s`,
		p.Name, p.Name, p.DOB, p.Education, p.Job, p.Family, p.Location,
		p.Personality, p.Interests, p.Relationship, p.Secret, p.Vibe,
		honorific, extra, searchContext)

	return persona, honorific
}

func parseAIJSON(raw string) (string, string, error) {
	// Bước 1: Tìm trong khối ```json ... ``` (Gemma hay trả về dạng này)
	if idx := strings.Index(raw, "```json"); idx != -1 {
		jsonStart := idx + 7 // bỏ qua "```json"
		if jsonEnd := strings.Index(raw[jsonStart:], "```"); jsonEnd != -1 {
			raw = strings.TrimSpace(raw[jsonStart : jsonStart+jsonEnd])
		}
	}

	// Bước 2: Tìm cặp {...} cuối cùng trong chuỗi (bỏ qua phần suy luận phía trước)
	lastEnd := strings.LastIndex(raw, "}")
	if lastEnd == -1 {
		return raw, "", nil
	}

	// Tìm dấu { mở tương ứng bằng cách đếm ngược
	depth := 0
	startPos := -1
	for i := lastEnd; i >= 0; i-- {
		if raw[i] == '}' {
			depth++
		} else if raw[i] == '{' {
			depth--
			if depth == 0 {
				startPos = i
				break
			}
		}
	}

	if startPos == -1 {
		return raw, "", nil
	}

	clean := raw[startPos : lastEnd+1]
	var parsed AIResponse
	if err := json.Unmarshal([]byte(clean), &parsed); err == nil {
		return parsed.Text, parsed.Reaction, nil
	}

	// Fallback: Tự bóc tách thủ công nếu JSON bị lỗi định dạng (thường do dấu ngoặc kép lồng nhau như "gà mới" mà không được escape)
	text := extractJSONField(clean, "text")
	reaction := extractJSONField(clean, "reaction")
	if text != "" {
		return text, reaction, nil
	}

	return raw, "", nil
}

func enforcePersonaText(text, reaction string, parseErr error, profile BotProfile, honorific string) string {
	if parseErr != nil {
		return text
	}
	lower := strings.ToLower(text)
	blockedIdentities := []string{
		"deepseek",
		"chatgpt",
		"claude",
		"gemini",
		"openai",
		"anthropic",
		"google ai",
		"trợ lý ai",
		"mô hình ai",
		"model ai",
	}
	for _, identity := range blockedIdentities {
		if strings.Contains(lower, identity) {
			if honorific == "" {
				honorific = "anh/chị"
			}
			return fmt.Sprintf("Chào %s, Vy đây. Có gì cần Vy hỗ trợ không?", honorific)
		}
	}
	return text
}

// extractJSONField trích xuất giá trị trường trong chuỗi JSON thủ công kể cả khi JSON bị lỗi nháy kép lồng nhau
func extractJSONField(jsonStr, key string) string {
	keyPattern := `"` + key + `"`
	idx := strings.Index(jsonStr, keyPattern)
	if idx == -1 {
		return ""
	}
	afterKey := jsonStr[idx+len(keyPattern):]
	colonIdx := strings.Index(afterKey, ":")
	if colonIdx == -1 {
		return ""
	}
	afterColon := afterKey[colonIdx+1:]
	startQuote := strings.Index(afterColon, `"`)
	if startQuote == -1 {
		return ""
	}
	valPart := afterColon[startQuote+1:]

	var result strings.Builder
	escaped := false
	for i := 0; i < len(valPart); i++ {
		char := valPart[i]
		if escaped {
			result.WriteByte(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			// Xác định xem đây có phải nháy kép kết thúc trường hay không
			isEnd := false
			for j := i + 1; j < len(valPart); j++ {
				nextChar := valPart[j]
				if nextChar == ' ' || nextChar == '\t' || nextChar == '\r' || nextChar == '\n' {
					continue
				}
				if nextChar == ',' || nextChar == '}' {
					isEnd = true
				}
				break
			}
			if i == len(valPart)-1 {
				isEnd = true
			}
			if isEnd {
				break
			}
		}
		result.WriteByte(char)
	}
	return strings.TrimSpace(result.String())
}
