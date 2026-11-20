package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tranhaonguyendev/za-go"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NotionAlertService only reads Notion. MongoDB keeps the last announced state
// so restarting the bot does not announce the same records again.
type NotionAlertService struct {
	Token        string
	DataSourceID string
	GroupID      string
	HTTP         *http.Client
	Collection   *mongo.Collection
}

type notionPage struct {
	ID         string                     `json:"id"`
	URL        string                     `json:"url"`
	Properties map[string]json.RawMessage `json:"properties"`
}

type notionQueryResponse struct {
	Results    []notionPage `json:"results"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor"`
}

type dossierSnapshot struct {
	ID        string    `bson:"_id"`
	Status    string    `bson:"status"`
	Name      string    `bson:"name"`
	UpdatedAt time.Time `bson:"updated_at"`
}

type dossierRecord struct {
	ID       string
	URL      string
	Name     string
	Project  string
	Status   string
	Holder   string
	SignedBy string
	NextSign string
}

func NewNotionAlertServiceFromEnv(db *DBService) *NotionAlertService {
	if db == nil {
		return nil
	}
	service := &NotionAlertService{
		Token:        strings.TrimSpace(os.Getenv("NOTION_API_KEY")),
		DataSourceID: strings.TrimSpace(os.Getenv("NOTION_DOSSIER_DATA_SOURCE_ID")),
		GroupID:      strings.TrimSpace(getenv("NOTION_ALERT_GROUP_ID", os.Getenv("LETTER_ALERT_GROUP_ID"))),
		HTTP:         &http.Client{Timeout: 20 * time.Second},
		Collection:   db.Client.Database("zalo_bot").Collection("notion_dossier_alerts"),
	}
	if service.Token == "" || service.DataSourceID == "" || service.GroupID == "" {
		return nil
	}
	return service
}

func (s *NotionAlertService) fetchRecords(ctx context.Context) ([]dossierRecord, error) {
	var records []dossierRecord
	cursor := ""
	for {
		body := map[string]any{"page_size": 100}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		url := "https://api.notion.com/v1/data_sources/" + s.DataSourceID + "/query"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.Token)
		req.Header.Set("Notion-Version", "2025-09-03")
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			resp.Body.Close()
			return nil, fmt.Errorf("Notion API trả %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
		}
		var result notionQueryResponse
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, page := range result.Results {
			record := dossierRecord{
				ID: page.ID, URL: page.URL,
				Name:     notionText(page.Properties["Hồ sơ"], "title"),
				Project:  notionText(page.Properties["Công trình"], "rich_text"),
				Status:   notionOption(page.Properties["Trạng thái"], "status"),
				Holder:   notionText(page.Properties["Người đang giữ"], "rich_text"),
				SignedBy: notionText(page.Properties["Đã ký bởi"], "rich_text"),
				NextSign: notionText(page.Properties["Đang chờ bên ký"], "rich_text"),
			}
			if record.Name != "" {
				records = append(records, record)
			}
		}
		if !result.HasMore {
			break
		}
		if result.NextCursor == "" {
			return nil, fmt.Errorf("Notion báo còn trang nhưng không có cursor")
		}
		cursor = result.NextCursor
	}
	return records, nil
}

func notionText(raw json.RawMessage, key string) string {
	var property map[string]json.RawMessage
	if json.Unmarshal(raw, &property) != nil {
		return ""
	}
	var parts []struct {
		PlainText string `json:"plain_text"`
	}
	if json.Unmarshal(property[key], &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(part.PlainText)
	}
	return strings.TrimSpace(b.String())
}

func notionOption(raw json.RawMessage, key string) string {
	var property map[string]json.RawMessage
	if json.Unmarshal(raw, &property) != nil {
		return ""
	}
	var option struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(property[key], &option) != nil {
		return ""
	}
	return strings.TrimSpace(option.Name)
}

func formatDossierAlert(record dossierRecord, previousStatus string, isNew bool) string {
	var b strings.Builder
	if isNew {
		fmt.Fprintf(&b, "📄 Hồ sơ mới: %s", record.Name)
	} else {
		fmt.Fprintf(&b, "📄 %s: %s → %s", record.Name, fallback(previousStatus, "chưa có trạng thái"), fallback(record.Status, "chưa có trạng thái"))
	}
	if record.Project != "" {
		fmt.Fprintf(&b, "\nCông trình: %s", record.Project)
	}
	if record.Status != "" && isNew {
		fmt.Fprintf(&b, "\nTrạng thái: %s", record.Status)
	}
	if record.SignedBy != "" {
		fmt.Fprintf(&b, "\nĐã ký: %s", record.SignedBy)
	}
	if record.NextSign != "" {
		fmt.Fprintf(&b, "\nChờ ký: %s", record.NextSign)
	}
	if record.Holder != "" {
		fmt.Fprintf(&b, "\nĐang giữ: %s", record.Holder)
	}
	if record.URL != "" {
		fmt.Fprintf(&b, "\nXem hồ sơ: %s", record.URL)
	}
	return b.String()
}

func (s *NotionAlertService) saveSnapshot(ctx context.Context, record dossierRecord) error {
	doc := dossierSnapshot{ID: record.ID, Status: record.Status, Name: record.Name, UpdatedAt: time.Now()}
	_, err := s.Collection.ReplaceOne(ctx, bson.M{"_id": record.ID}, doc, options.Replace().SetUpsert(true))
	return err
}

func (s *NotionAlertService) poll(client *zago.ZaloAPI) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	records, err := s.fetchRecords(ctx)
	if err != nil {
		return err
	}
	var baseline dossierSnapshot
	err = s.Collection.FindOne(ctx, bson.M{"_id": "__baseline__"}).Decode(&baseline)
	if err != nil && err != mongo.ErrNoDocuments {
		return err
	}
	// First run establishes a baseline; old records are not announced as new.
	if err == mongo.ErrNoDocuments {
		for _, record := range records {
			if err := s.saveSnapshot(ctx, record); err != nil {
				return err
			}
		}
		_, err = s.Collection.InsertOne(ctx, dossierSnapshot{ID: "__baseline__", UpdatedAt: time.Now()})
		if err != nil {
			return err
		}
		log.Printf("Notion: đã ghi mốc ban đầu cho %d hồ sơ", len(records))
		return nil
	}
	for _, record := range records {
		var previous dossierSnapshot
		err := s.Collection.FindOne(ctx, bson.M{"_id": record.ID}).Decode(&previous)
		isNew := err == mongo.ErrNoDocuments
		if err != nil && !isNew {
			return err
		}
		if !isNew && previous.Status == record.Status {
			continue
		}
		message := formatDossierAlert(record, previous.Status, isNew)
		if _, err := client.SendMessage(zago.Message{Text: message}, s.GroupID, zago.ThreadTypeGROUP); err != nil {
			log.Printf("Notion: chưa gửi được hồ sơ %s: %v", record.ID, err)
			continue // Retry on the next poll; do not mark an unsent alert as delivered.
		}
		if err := s.saveSnapshot(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (s *NotionAlertService) Start(client *zago.ZaloAPI) {
	if s == nil {
		return
	}
	go func() {
		// Polling keeps the integration simple and works without a public webhook URL.
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			if err := s.poll(client); err != nil {
				log.Printf("Notion: lỗi kiểm tra hồ sơ: %v", err)
			}
			<-ticker.C
		}
	}()
}
