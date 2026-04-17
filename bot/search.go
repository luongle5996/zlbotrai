package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type SearchResult struct {
	Title string `json:"title"`
	Link  string `json:"link"`
	Snippet string `json:"snippet"`
}

type SerperResponse struct {
	Organic []SearchResult `json:"organic"`
}

type SearchService struct {
	APIKey string
}

func NewSearchService(apiKey string) *SearchService {
	return &SearchService{
		APIKey: apiKey,
	}
}

func (s *SearchService) SearchWeb(query string) (string, error) {
	url := "https://google.serper.dev/search"
	payload := map[string]string{
		"q": query,
		"num": "1", // Chỉ lấy 1 kết quả theo yêu cầu của user
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("X-API-KEY", s.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Serper API error (%d): %s", resp.StatusCode, string(body))
	}

	var serperResp SerperResponse
	if err := json.Unmarshal(body, &serperResp); err != nil {
		return "", err
	}

	if len(serperResp.Organic) > 0 {
		res := serperResp.Organic[0]
		return fmt.Sprintf("Nguồn: %s\nNội dung chính: %s\nLink: %s", res.Title, res.Snippet, res.Link), nil
	}

	return "Không tìm thấy kết quả nào liên quan.", nil
}
