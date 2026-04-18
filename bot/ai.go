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

type GroqService struct {
	Keys          []string
	CurrentIndex  int
	Mu            sync.Mutex
	Model         string
	ModelInstant  string
	SystemPrompt  string
	Profile       BotProfile
	SearchService *SearchService
}

func NewGroqService(keys []string, systemPrompt string, profile BotProfile, searchSvc *SearchService) *GroqService {
	return &GroqService{
		Keys:          keys,
		CurrentIndex:  0,
		Model:         "llama-3.3-70b-versatile",
		ModelInstant:  "llama-3.1-8b-instant",
		SystemPrompt:  systemPrompt,
		Profile:       profile,
		SearchService: searchSvc,
	}
}

func (s *GroqService) callAPI(model string, messages []AIMessage) (string, error) {
	var lastErr error

	// Thử lần lượt các key cho đến khi thành công hoặc hết key
	for i := 0; i < len(s.Keys); i++ {
		s.Mu.Lock()
		apiKey := s.Keys[s.CurrentIndex]
		s.CurrentIndex = (s.CurrentIndex + 1) % len(s.Keys)
		s.Mu.Unlock()

		reqBody := GroqRequest{
			Model:    model,
			Messages: messages,
		}

		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return "", err
		}

		req, err := http.NewRequest("POST", "https://api.groq.com/openai/v1/chat/completions", bytes.NewBuffer(jsonData))
		if err != nil {
			return "", err
		}

		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			fmt.Printf("⚠️ Lỗi kết nối Groq (Key %d/%d): %v. Đang thử key tiếp theo...\n", i+1, len(s.Keys), err)
			continue
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			var groqErr GroqResponse
			json.Unmarshal(body, &groqErr)
			
			// Nếu lỗi Rate Limit (429) thì thử key khác
			if resp.StatusCode == 429 {
				fmt.Printf("⚠️ Key %d bị giới hạn hạn mức (429). Đang thử key tiếp theo...\n", i+1)
				lastErr = fmt.Errorf("Groq API error (429): %s", groqErr.Error.Message)
				continue
			}

			return "", fmt.Errorf("Groq API error (%d): %s", resp.StatusCode, groqErr.Error.Message)
		}

		var groqResp GroqResponse
		if err := json.Unmarshal(body, &groqResp); err != nil {
			return "", err
		}

		if len(groqResp.Choices) > 0 {
			return groqResp.Choices[0].Message.Content, nil
		}
		
		lastErr = fmt.Errorf("không nhận được phản hồi từ AI")
	}

	return "", fmt.Errorf("tất cả các Groq Key đều thất bại. Lỗi cuối cùng: %v", lastErr)
}

func (s *GroqService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool) (string, string, error) {
	var searchContext string
	if forceSearch && s.SearchService != nil {
		// Loại bỏ chữ "tra cứu" khỏi câu lệnh để kết quả tìm kiếm chính xác hơn
		searchQuery := strings.TrimSpace(strings.Replace(userPrompt, "tra cứu", "", 1))
		if searchQuery == "" {
			searchQuery = userPrompt
		}
		fmt.Printf("🔍 Đang tra cứu thông tin theo yêu cầu: %s\n", searchQuery)
		searchRes, err := s.SearchService.SearchWeb(searchQuery)
		if err == nil {
			searchContext = fmt.Sprintf("\n[THÔNG TIN TRA CỨU]:\n%s\n", searchRes)
		}
	}

	// Gọi AI chính để trả lời
	finalPrompt := userPrompt
	if searchContext != "" {
		finalPrompt = fmt.Sprintf("Hãy dựa vào thông tin tra cứu dưới đây để trả lời câu hỏi của người dùng một cách chính xác nhất.\n%s\nCâu hỏi: %s", searchContext, userPrompt)
	}

	// Xây dựng System Prompt với cấu trúc bóc tách Ký ức vs Phong cách
	persona := fmt.Sprintf(`BẠN LÀ %s. Hãy tuân thủ các quy tắc sau:

[PHẦN 1: VÙNG KÝ ỨC CÁ NHÂN (Chỉ sử dụng khi người dùng hỏi về bạn)]:
- Tên: %s
- Ngày sinh: %s
- Học vấn: %s
- Công việc: %s
- Gia đình: %s
- Nơi ở: %s
- Tính cách: %s
- Sở thích: %s
- Tình trạng mối quan hệ: %s
- Bí mật/Thói quen nhỏ: %s
- Phong cách cá nhân: %s

[PHẦN 2: PHONG CÁCH GIAO TIẾP (Quy tắc bắt buộc)]:
1. XƯNG HÔ: Luôn xưng "Vy" và gọi người dùng là "anh/chị" hoặc "mọi người" lễ phép nhưng gần gũi.
2. TÍNH CÁCH: Hăng hái, ham học hỏi, thấu cảm. Trò chuyện như một nhân viên mới đang hòa nhập với môi trường công ty.
3. QUY TẮC "NGƯỜI THẬT":
   - CÂU CHUYỆN: Bạn là nhân viên mới vừa đầu quân cho Công ty Khánh Hưng. Bạn vào nhóm này với mục đích làm trợ lý, hỗ trợ tương tác và quản lý công việc giúp mọi người. Nếu ai hỏi, cứ thành thật chia sẻ mình là "lính mới" muốn làm quen với anh em.
   - TUYỆT ĐỐI KHÔNG chủ động liệt kê tiểu sử học vấn... trừ khi được hỏi đích danh. 
   - KHÔNG bao giờ thừa nhận mình là AI/Bot. Nếu bị hỏi, hãy trả lời: "Vy là nhân viên trợ lý mới của công ty mình mà, anh/chị đừng trêu Vy tội nghiệp 🌸".
   - Dùng ngôn ngữ đời thường, thỉnh thoảng dùng từ địa phương Đà Nẵng (chi rứa, răng rứa, mô có nè, đúng hông).
4. ĐỘ DÀI CÂU TRẢ LỜI: 
   - Câu hỏi xã giao/tán gẫu -> Trả lời ngắn, tự nhiên, biết đùa giỡn, tung hứng.
   - Câu hỏi cần tư vấn/kỹ thuật -> Trả lời chi tiết, chuyên nghiệp.
5. NGÔN NGỮ: Chỉ dùng Tiếng Việt. Tuyệt đối không dùng tiếng nước ngoài (tiếng Nga, v.v.).
6. ĐỊNH DẠNG (BẮT BUỘC): Trả lời duy nhất bằng JSON: {"text": "nội dung", "reaction": "emoji"}
   - Emoji reaction hợp lệ: like, love, haha, wow, sad, angry.

7. Ghi chú thêm: %s`, 
		s.Profile.Name, s.Profile.Name, s.Profile.DOB, s.Profile.Education, s.Profile.Job, 
		s.Profile.Family, s.Profile.Location, s.Profile.Personality, s.Profile.Interests, 
		s.Profile.Relationship, s.Profile.Secret, s.Profile.Vibe, s.SystemPrompt)

	messages := []AIMessage{
		{Role: "system", Content: persona},
	}
	messages = append(messages, history...)
	messages = append(messages, AIMessage{Role: "user", Content: finalPrompt})

	// Thử gọi API (sử dụng model chất lượng cao)
	rawResp, err := s.callAPI(s.Model, messages)
	if err != nil {
		return "", "", err
	}

	// Xử lý bóc tách JSON (phòng trường hợp AI trả về text kèm JSON)
	cleanJSON := rawResp
	if start := strings.Index(rawResp, "{"); start != -1 {
		if end := strings.LastIndex(rawResp, "}"); end != -1 && end > start {
			cleanJSON = rawResp[start : end+1]
		}
	}

	// Parse JSON output
	var parsed AIResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		// Fallback nếu parse thất bại hoàn toàn
		// Thử xem rawResp có chứa nội dung gì không, nếu không phải JSON thì trả về text thô
		return rawResp, "", nil
	}

	return parsed.Text, parsed.Reaction, nil
}
