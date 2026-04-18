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

type GeminiRequest struct {
	Contents []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	SystemInstruction struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"system_instruction"`
	GenerationConfig struct {
		ResponseMimeType string `json:"response_mime_type"`
	} `json:"generation_config"`
}

func (s *GeminiService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool, honorific string) (string, string, error) {
	systemPrompt, _ := buildFullPrompt(userPrompt, s.Profile, s.SystemPrompt, s.SearchService, forceSearch, honorific)

	req := GeminiRequest{}
	req.SystemInstruction.Parts = append(req.SystemInstruction.Parts, struct {
		Text string `json:"text"`
	}{Text: systemPrompt})
	req.GenerationConfig.ResponseMimeType = "application/json"

	for _, m := range history {
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		req.Contents = append(req.Contents, struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}{Role: role, Parts: []struct {
			Text string `json:"text"`
		}{{Text: m.Content}}})
	}
	req.Contents = append(req.Contents, struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	}{Role: "user", Parts: []struct {
		Text string `json:"text"`
	}{{Text: userPrompt}}})

	jsonData, _ := json.Marshal(req)
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
			} `json:"candidates"`
		}
		json.Unmarshal(body, &geminiResp)

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			return parseAIJSON(geminiResp.Candidates[0].Content.Parts[0].Text)
		}
	}

	return "", "", fmt.Errorf("tất cả các Gemini Key đều thất bại: %v", lastErr)
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
5. ĐỊNH DẠNG: Luôn trả về JSON: {"text": "...", "reaction": "emoji"}
   (Emoji: like, love, haha, wow, sad, angry)

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
