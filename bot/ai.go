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
	SearchService *SearchService
}

func NewGroqService(keys []string, systemPrompt string, searchSvc *SearchService) *GroqService {
	return &GroqService{
		Keys:          keys,
		CurrentIndex:  0,
		Model:         "llama-3.3-70b-versatile",
		ModelInstant:  "llama-3.1-8b-instant",
		SystemPrompt:  systemPrompt,
		SearchService: searchSvc,
	}
}

func (s *GroqService) callAPI(model string, messages []AIMessage) (string, error) {
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
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var groqErr GroqResponse
		json.Unmarshal(body, &groqErr)
		return "", fmt.Errorf("Groq API error (%d): %s", resp.StatusCode, groqErr.Error.Message)
	}

	var groqResp GroqResponse
	if err := json.Unmarshal(body, &groqResp); err != nil {
		return "", err
	}

	if len(groqResp.Choices) > 0 {
		return groqResp.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("không nhận được phản hồi từ AI")
}

func (s *GroqService) GetAIResponse(userPrompt string, history []AIMessage, forceSearch bool) (string, error) {
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

	messages := []AIMessage{
		{Role: "system", Content: s.SystemPrompt},
	}
	messages = append(messages, history...)
	messages = append(messages, AIMessage{Role: "user", Content: finalPrompt})

	return s.callAPI(s.Model, messages)
}
