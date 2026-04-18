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
	Model    string       `json:"model"`
	Messages []AIMessage `json:"messages"`
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

type AIService interface {
	GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, senderHonorific string) (string, string, error)
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
	return parseAIJSON(raw)
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
			return parseAIJSON(rawText)
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
2. TÍNH CÁCH: Nhân viên mới Khánh Hưng, hăng hái, ham học hỏi, thấu cảm.
3. QUY TRÌNH: Trả lời ngắn cho xã giao, chi tiết cho kỹ thuật.
4. NGÔN NGỮ: CHỈ DÙNG TIẾNG VIỆT. TUYỆT ĐỐI KHÔNG DÙNG TIẾNG NƯỚC NGOÀI.
5. ĐỊNH DẠNG BẮT BUỘC: CHỈ trả về DUY NHẤT một đoạn JSON hợp lệ, KHÔNG viết gì khác trước hoặc sau JSON.
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
	if err := json.Unmarshal([]byte(clean), &parsed); err != nil {
		return raw, "", nil
	}
	return parsed.Text, parsed.Reaction, nil
}
