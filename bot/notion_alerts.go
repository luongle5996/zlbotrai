package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
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
	NewAlertTime string
	UpdateWait   time.Duration
	HTTP         *http.Client
	Collection   *mongo.Collection
}

type notionPage struct {
	ID             string                     `json:"id"`
	URL            string                     `json:"url"`
	CreatedTime    time.Time                  `json:"created_time"`
	LastEditedTime time.Time                  `json:"last_edited_time"`
	Properties     map[string]json.RawMessage `json:"properties"`
}

type notionQueryResponse struct {
	Results    []notionPage `json:"results"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor"`
}

type dossierSnapshot struct {
	ID           string    `bson:"_id"`
	Status       string    `bson:"status"`
	Name         string    `bson:"name"`
	UpdatedAt    time.Time `bson:"updated_at"`
	FirstSeen    time.Time `bson:"first_seen"`
	NewAnnounced bool      `bson:"new_announced"`
	Signature    string    `bson:"signature"`
}

type dossierRecord struct {
	ID           string
	CreatedAt    time.Time
	URL          string
	Name         string
	Status       string
	SentDate     string
	Recipient    string
	Action       string
	Holder       string
	SignedBy     string
	NextSign     string
	LastEditedAt time.Time
}

func NewNotionAlertServiceFromEnv(db *DBService) *NotionAlertService {
	if db == nil {
		return nil
	}
	service := &NotionAlertService{
		Token:        strings.TrimSpace(os.Getenv("NOTION_API_KEY")),
		DataSourceID: strings.TrimSpace(os.Getenv("NOTION_DOSSIER_DATA_SOURCE_ID")),
		GroupID:      strings.TrimSpace(getenv("NOTION_ALERT_GROUP_ID", os.Getenv("LETTER_ALERT_GROUP_ID"))),
		NewAlertTime: strings.TrimSpace(getenv("NOTION_NEW_ALERT_TIME", "18:00")),
		UpdateWait:   updateWaitFromEnv(os.Getenv("NOTION_UPDATE_WAIT_MINUTES")),
		HTTP:         &http.Client{Timeout: 20 * time.Second},
		Collection:   db.Client.Database("zalo_bot").Collection("notion_dossier_alerts"),
	}
	if service.Token == "" || service.DataSourceID == "" || service.GroupID == "" {
		return nil
	}
	return service
}

func updateWaitFromEnv(raw string) time.Duration {
	minutes, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || minutes < 1 || minutes > 60 {
		return time.Minute
	}
	return time.Duration(minutes) * time.Minute
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
				ID: page.ID, URL: page.URL, CreatedAt: page.CreatedTime, LastEditedAt: page.LastEditedTime,
				Name:      notionText(page.Properties["Hồ sơ"], "title"),
				Status:    notionOption(page.Properties["Trạng thái"], "status"),
				SentDate:  notionDate(page.Properties["Ngày gửi lên"]),
				Recipient: notionText(page.Properties["Người nhận tại công trình"], "rich_text"),
				Action:    notionText(page.Properties["Yêu cầu xử lý"], "rich_text"),
				Holder:    notionText(page.Properties["Người đang giữ"], "rich_text"),
				SignedBy:  notionText(page.Properties["Đã ký bởi"], "rich_text"),
				NextSign:  notionText(page.Properties["Đang chờ bên ký"], "rich_text"),
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

func notionDate(raw json.RawMessage) string {
	var property struct {
		Date *struct {
			Start string `json:"start"`
		} `json:"date"`
	}
	if json.Unmarshal(raw, &property) != nil || property.Date == nil {
		return ""
	}
	start := property.Date.Start
	if len(start) < 10 {
		return start
	}
	date, err := time.Parse("2006-01-02", start[:10])
	if err != nil {
		return start
	}
	return date.Format("02/01/2006")
}

func vietnamTime(at time.Time) string {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("ICT", 7*60*60)
	}
	return at.In(loc).Format("15:04 02/01/2006")
}

func formatDossierAlert(record dossierRecord, previousStatus string, isNew bool) string {
	var b strings.Builder
	label := record.Name
	if record.SentDate != "" {
		label = record.SentDate + " — " + label
	}
	if isNew {
		fmt.Fprintf(&b, "📄 Hồ sơ mới: %s", label)
	} else if previousStatus == record.Status {
		fmt.Fprintf(&b, "📄 Cập nhật hồ sơ: %s", label)
	} else {
		fmt.Fprintf(&b, "📄 %s: %s → %s", label, fallback(previousStatus, "chưa có trạng thái"), fallback(record.Status, "chưa có trạng thái"))
	}
	if record.Recipient != "" {
		fmt.Fprintf(&b, "\nNgười nhận tại công trình: %s", record.Recipient)
	}
	if record.Action != "" {
		fmt.Fprintf(&b, "\nCần làm: %s", record.Action)
	}
	if record.Status != "" && (isNew || previousStatus == record.Status) {
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
	fmt.Fprintf(&b, "\nBot báo lúc: %s", vietnamTime(time.Now()))
	return b.String()
}

func formatNewDossiers(records []dossierRecord, at time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📄 %d hồ sơ mới — bot báo lúc %s", len(records), vietnamTime(at))
	for i, record := range records {
		fmt.Fprintf(&b, "\n\n%d. ", i+1)
		if record.SentDate != "" {
			fmt.Fprintf(&b, "%s — ", record.SentDate)
		}
		b.WriteString(record.Name)
		if record.Recipient != "" {
			fmt.Fprintf(&b, "\nNgười nhận tại công trình: %s", record.Recipient)
		}
		if record.Action != "" {
			fmt.Fprintf(&b, "\nCần làm: %s", record.Action)
		}
		if record.Status != "" {
			fmt.Fprintf(&b, "\nTrạng thái: %s", record.Status)
		}
		if record.NextSign != "" {
			fmt.Fprintf(&b, "\nChờ ký: %s", record.NextSign)
		}
		if record.URL != "" {
			fmt.Fprintf(&b, "\n%s", record.URL)
		}
	}
	return b.String()
}

func (s *NotionAlertService) saveSnapshot(ctx context.Context, record dossierRecord, announced bool) error {
	firstSeen := record.CreatedAt
	if firstSeen.IsZero() {
		firstSeen = time.Now()
	}
	doc := dossierSnapshot{ID: record.ID, Status: record.Status, Name: record.Name, UpdatedAt: time.Now(), FirstSeen: firstSeen, NewAnnounced: announced, Signature: dossierSignature(record)}
	_, err := s.Collection.ReplaceOne(ctx, bson.M{"_id": record.ID}, doc, options.Replace().SetUpsert(true))
	return err
}

func (s *NotionAlertService) updateStatus(ctx context.Context, record dossierRecord) error {
	_, err := s.Collection.UpdateOne(ctx, bson.M{"_id": record.ID}, bson.M{"$set": bson.M{"status": record.Status, "name": record.Name, "signature": dossierSignature(record), "updated_at": time.Now()}})
	return err
}

func dossierSignature(record dossierRecord) string {
	data, _ := json.Marshal([]string{record.Status, record.Holder, record.SignedBy, record.NextSign, record.Recipient, record.Action})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func readyForUpdate(record dossierRecord, previous dossierSnapshot, now time.Time, wait time.Duration) bool {
	return previous.NewAnnounced && previous.Signature != "" && previous.Signature != dossierSignature(record) &&
		!record.LastEditedAt.IsZero() && !record.LastEditedAt.After(now.Add(-wait))
}

type dossierChange struct {
	Record         dossierRecord
	PreviousStatus string
}

func formatUpdatedDossiers(changes []dossierChange, at time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📄 %d hồ sơ cập nhật — bot báo lúc %s", len(changes), vietnamTime(at))
	for i, change := range changes {
		body := formatDossierAlert(change.Record, change.PreviousStatus, false)
		body = strings.TrimPrefix(body, "📄 ")
		if index := strings.LastIndex(body, "\nBot báo lúc:"); index >= 0 {
			body = body[:index]
		}
		fmt.Fprintf(&b, "\n\n%d. %s", i+1, body)
	}
	return b.String()
}

func digestCutoff(now time.Time, alertTime string) (time.Time, bool) {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.FixedZone("ICT", 7*60*60)
	}
	hour, minute := 18, 0
	if parsed, err := time.Parse("15:04", alertTime); err == nil {
		hour, minute = parsed.Hour(), parsed.Minute()
	}
	local := now.In(loc)
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	return cutoff, !local.Before(cutoff)
}

func (s *NotionAlertService) sendDailyDigest(ctx context.Context, client *zago.ZaloAPI, records []dossierRecord, now time.Time) error {
	cutoff, due := digestCutoff(now, s.NewAlertTime)
	if !due {
		return nil
	}
	dateKey := cutoff.Format("2006-01-02")
	var marker dossierSnapshot
	err := s.Collection.FindOne(ctx, bson.M{"_id": "__digest__"}).Decode(&marker)
	if err != nil && err != mongo.ErrNoDocuments {
		return err
	}
	if err == nil && marker.Name == dateKey {
		return nil
	}
	var pending []dossierRecord
	for _, record := range records {
		var snapshot dossierSnapshot
		if err := s.Collection.FindOne(ctx, bson.M{"_id": record.ID}).Decode(&snapshot); err != nil {
			return err
		}
		if !snapshot.NewAnnounced && !snapshot.FirstSeen.IsZero() && !snapshot.FirstSeen.After(cutoff) {
			pending = append(pending, record)
		}
	}
	for start := 0; start < len(pending); start += 5 {
		end := start + 5
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]
		if _, err := client.SendMessage(zago.Message{Text: formatNewDossiers(batch, now)}, s.GroupID, zago.ThreadTypeGROUP); err != nil {
			return fmt.Errorf("gửi bản tin hồ sơ mới: %w", err)
		}
		for _, record := range batch {
			if _, err := s.Collection.UpdateOne(ctx, bson.M{"_id": record.ID}, bson.M{"$set": bson.M{"new_announced": true}}); err != nil {
				return err
			}
		}
	}
	_, err = s.Collection.ReplaceOne(ctx, bson.M{"_id": "__digest__"}, dossierSnapshot{ID: "__digest__", Name: dateKey, UpdatedAt: now}, options.Replace().SetUpsert(true))
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
			if err := s.saveSnapshot(ctx, record, true); err != nil {
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
	var pending []dossierChange
	var latestEdit time.Time
	for _, record := range records {
		var previous dossierSnapshot
		err := s.Collection.FindOne(ctx, bson.M{"_id": record.ID}).Decode(&previous)
		isNew := err == mongo.ErrNoDocuments
		if err != nil && !isNew {
			return err
		}
		if isNew {
			if err := s.saveSnapshot(ctx, record, false); err != nil {
				return err
			}
			continue
		}
		// Older snapshots have no signature: establish a baseline without sending old changes.
		if previous.Signature == "" {
			if err := s.updateStatus(ctx, record); err != nil {
				return err
			}
			continue
		}
		if !previous.NewAnnounced || previous.Signature == dossierSignature(record) {
			continue
		}
		pending = append(pending, dossierChange{Record: record, PreviousStatus: previous.Status})
		if record.LastEditedAt.After(latestEdit) {
			latestEdit = record.LastEditedAt
		}
	}
	// Wait until the whole batch is quiet, then send one message per five records.
	if len(pending) > 0 && !latestEdit.IsZero() && !latestEdit.After(time.Now().Add(-s.UpdateWait)) {
		for start := 0; start < len(pending); start += 5 {
			end := start + 5
			if end > len(pending) {
				end = len(pending)
			}
			batch := pending[start:end]
			if _, err := client.SendMessage(zago.Message{Text: formatUpdatedDossiers(batch, time.Now())}, s.GroupID, zago.ThreadTypeGROUP); err != nil {
				log.Printf("Notion: chưa gửi được nhóm %d hồ sơ cập nhật: %v", len(batch), err)
				continue
			}
			for _, change := range batch {
				if err := s.updateStatus(ctx, change.Record); err != nil {
					return err
				}
			}
		}
	}
	return s.sendDailyDigest(ctx, client, records, time.Now())
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
