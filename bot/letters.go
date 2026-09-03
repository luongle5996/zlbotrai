package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tranhaonguyendev/za-go"
)

type LetterService struct {
	CSVURL       string
	AlertGroupID string
	AlertDays    int
	AlertTime    string
	Location     *time.Location
}

type ContractLetter struct {
	ContractCode string
	ContractName string
	LetterNo     string
	LetterType   string
	Amount       string
	IssueDate    string
	ExpiryDate   time.Time
	Status       string
	OriginalNo   string
	RevisionNo   string
	Note         string
}

func NewLetterServiceFromEnv(loc *time.Location) *LetterService {
	alertDays := 30
	if raw := strings.TrimSpace(getenv("LETTER_ALERT_DAYS", "")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			alertDays = parsed
		}
	}

	return &LetterService{
		CSVURL:       strings.TrimSpace(getenv("LETTER_SHEET_CSV_URL", "")),
		AlertGroupID: strings.TrimSpace(getenv("LETTER_ALERT_GROUP_ID", "")),
		AlertDays:    alertDays,
		AlertTime:    strings.TrimSpace(getenv("LETTER_ALERT_TIME", "08:00")),
		Location:     loc,
	}
}

func (s *LetterService) Enabled() bool {
	return s != nil && s.CSVURL != ""
}

func (s *LetterService) AutoAlertEnabled() bool {
	return s.Enabled() && s.AlertGroupID != ""
}

func (s *LetterService) HandleMessage(message string) (string, bool) {
	if !s.Enabled() {
		return "", false
	}

	query := normalizeVietnamese(strings.ToLower(strings.TrimSpace(message)))
	lookupAt := strings.Index(query, "tracuu")
	if lookupAt < 0 {
		lookupAt = strings.Index(query, "tra cuu")
	}
	if lookupAt < 0 {
		return "", false
	}
	// Zalo may leave the full @mention in the message; it is not part of the lookup.
	query = strings.TrimSpace(query[lookupAt:])
	if !strings.Contains(query, "thu") && !strings.Contains(query, "hop dong") {
		return "", false
	}

	letters, err := s.FetchLetters()
	if err != nil {
		return fmt.Sprintf("Em chưa đọc được Google Sheet thư hợp đồng: %v", err), true
	}

	today := todayIn(s.Location)
	keyword := extractLookupKeyword(query)
	if isDueListQuery(query) && keyword == "" {
		due := s.FilterDueLetters(letters, today)
		return s.FormatDueLetters(due, today, false), true
	}

	if keyword == "" {
		return "Anh/chị hỏi em theo mẫu: \"Vy thư gần hết hạn\" hoặc \"Vy thư BL-001\" hoặc \"Vy hợp đồng HD001\" nhé.", true
	}

	matches := filterLettersByKeyword(letters, keyword)
	if len(matches) == 0 {
		return fmt.Sprintf("Em chưa thấy thư/hợp đồng nào khớp với %q trong Google Sheet.", keyword), true
	}
	return formatLetterList(matches, today), true
}

func isDueListQuery(query string) bool {
	return strings.Contains(query, "gan het han") ||
		strings.Contains(query, "sap het han") ||
		strings.Contains(query, "thu nao") ||
		strings.Contains(query, "nhung thu") ||
		strings.Contains(query, "con han bao nhieu") ||
		strings.Contains(query, "het han")
}

func (s *LetterService) FetchLetters() ([]ContractLetter, error) {
	req, err := http.NewRequest(http.MethodGet, s.CSVURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "zalo-letter-bot/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Google Sheet trả mã %d", resp.StatusCode)
	}

	reader := csv.NewReader(resp.Body)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, nil
	}

	header := mapHeaders(rows[0])
	var letters []ContractLetter
	for _, row := range rows[1:] {
		expiryRaw := cell(row, header, "ngay_het_han")
		expiry, err := parseSheetDate(expiryRaw, s.Location)
		if err != nil {
			continue
		}
		letterNo := cell(row, header, "so_thu")
		contractCode := cell(row, header, "ma_hop_dong")
		if letterNo == "" && contractCode == "" {
			continue
		}

		letters = append(letters, ContractLetter{
			ContractCode: contractCode,
			ContractName: cell(row, header, "ten_hop_dong"),
			LetterNo:     letterNo,
			LetterType:   cell(row, header, "loai_thu"),
			Amount:       cell(row, header, "so_tien"),
			IssueDate:    cell(row, header, "ngay_phat_hanh"),
			ExpiryDate:   expiry,
			Status:       cell(row, header, "trang_thai"),
			OriginalNo:   cell(row, header, "thu_goc"),
			RevisionNo:   cell(row, header, "lan_tu_chinh"),
			Note:         cell(row, header, "ghi_chu"),
		})
	}

	sort.Slice(letters, func(i, j int) bool {
		return letters[i].ExpiryDate.Before(letters[j].ExpiryDate)
	})
	return letters, nil
}

func (s *LetterService) FilterDueLetters(letters []ContractLetter, today time.Time) []ContractLetter {
	var due []ContractLetter
	for _, letter := range letters {
		if !isActiveLetter(letter.Status) {
			continue
		}
		daysLeft := int(letter.ExpiryDate.Sub(today).Hours() / 24)
		if daysLeft >= 0 && daysLeft <= s.AlertDays {
			due = append(due, letter)
		}
	}
	return due
}

func (s *LetterService) FormatDueLetters(letters []ContractLetter, today time.Time, scheduled bool) string {
	if len(letters) == 0 {
		if scheduled {
			return ""
		}
		return fmt.Sprintf("Hiện không có thư nào còn hạn trong %d ngày tới.", s.AlertDays)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Thư gần hết hạn trong %d ngày tới:\n", s.AlertDays)
	for i, letter := range letters {
		daysLeft := int(letter.ExpiryDate.Sub(today).Hours() / 24)
		fmt.Fprintf(&b, "\n%d. HĐ: %s", i+1, fallback(letter.ContractCode, "chưa có mã"))
		if letter.ContractName != "" {
			fmt.Fprintf(&b, " - %s", letter.ContractName)
		}
		fmt.Fprintf(&b, "\nSố thư: %s", fallback(letter.LetterNo, "chưa có"))
		if letter.LetterType != "" {
			fmt.Fprintf(&b, "\nLoại thư: %s", letter.LetterType)
		}
		if letter.OriginalNo != "" {
			fmt.Fprintf(&b, "\nTu chỉnh từ: %s", letter.OriginalNo)
		}
		if letter.Amount != "" {
			fmt.Fprintf(&b, "\nSố tiền: %s", letter.Amount)
		}
		fmt.Fprintf(&b, "\nHết hạn: %s", letter.ExpiryDate.Format("02/01/2006"))
		fmt.Fprintf(&b, "\nCòn: %d ngày\n", daysLeft)
	}
	return strings.TrimSpace(b.String())
}

func (s *LetterService) StartDailyAlert(client *zago.ZaloAPI) {
	if !s.AutoAlertEnabled() {
		return
	}

	go func() {
		for {
			next := nextRunTime(s.AlertTime, s.Location)
			time.Sleep(time.Until(next))

			letters, err := s.FetchLetters()
			if err != nil {
				log.Printf("⚠️ Lỗi đọc Google Sheet thư hợp đồng: %v", err)
				continue
			}

			msg := s.FormatDueLetters(s.FilterDueLetters(letters, todayIn(s.Location)), todayIn(s.Location), true)
			if msg == "" {
				continue
			}
			if _, err := client.SendMessage(zago.Message{Text: msg}, s.AlertGroupID, zago.ThreadTypeGROUP); err != nil {
				log.Printf("⚠️ Lỗi gửi nhắc hạn thư vào nhóm %s: %v", s.AlertGroupID, err)
			}
		}
	}()
}

func formatLetterList(letters []ContractLetter, today time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Em tìm thấy %d thư/hợp đồng khớp:\n", len(letters))
	for i, letter := range letters {
		daysLeft := int(letter.ExpiryDate.Sub(today).Hours() / 24)
		fmt.Fprintf(&b, "\n%d. HĐ: %s", i+1, fallback(letter.ContractCode, "chưa có mã"))
		if letter.ContractName != "" {
			fmt.Fprintf(&b, " - %s", letter.ContractName)
		}
		fmt.Fprintf(&b, "\nSố thư: %s", fallback(letter.LetterNo, "chưa có"))
		if letter.OriginalNo != "" {
			fmt.Fprintf(&b, "\nTu chỉnh từ: %s", letter.OriginalNo)
		}
		if letter.RevisionNo != "" {
			fmt.Fprintf(&b, "\nLần tu chỉnh: %s", letter.RevisionNo)
		}
		if letter.Amount != "" {
			fmt.Fprintf(&b, "\nSố tiền: %s", letter.Amount)
		}
		fmt.Fprintf(&b, "\nHết hạn: %s", letter.ExpiryDate.Format("02/01/2006"))
		if daysLeft >= 0 {
			fmt.Fprintf(&b, "\nCòn: %d ngày", daysLeft)
		} else {
			fmt.Fprintf(&b, "\nĐã quá hạn: %d ngày", -daysLeft)
		}
		if letter.Status != "" {
			fmt.Fprintf(&b, "\nTrạng thái: %s", letter.Status)
		}
		fmt.Fprintln(&b)
	}
	return strings.TrimSpace(b.String())
}

func mapHeaders(row []string) map[string]int {
	headers := make(map[string]int)
	for i, value := range row {
		name := normalizeHeader(value)
		headers[name] = i
		// Accept the Vietnamese labels used in the Google Sheet template.
		switch name {
		case "so_hop_dong":
			headers["ma_hop_dong"] = i
		case "ten_du_an":
			headers["ten_hop_dong"] = i
		case "so_thu":
			headers["so_thu"] = i
		case "loai_thu":
			headers["loai_thu"] = i
		}
	}
	return headers
}

func normalizeHeader(value string) string {
	value = normalizeVietnamese(strings.ToLower(strings.TrimSpace(value)))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	return value
}

func cell(row []string, header map[string]int, name string) string {
	idx, ok := header[name]
	if !ok || idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func parseSheetDate(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, fmt.Errorf("ngày rỗng")
	}

	layouts := []string{"02/01/2006", "2/1/2006", "2006-01-02", "02-01-2006", "2-1-2006"}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return todayInTime(parsed, loc), nil
		}
	}

	if serial, err := strconv.ParseFloat(value, 64); err == nil {
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, loc)
		return base.AddDate(0, 0, int(serial)), nil
	}
	return time.Time{}, fmt.Errorf("không hiểu ngày %q", value)
}

func filterLettersByKeyword(letters []ContractLetter, keyword string) []ContractLetter {
	keyword = normalizeVietnamese(strings.ToLower(strings.TrimSpace(keyword)))
	var matches []ContractLetter
	for _, letter := range letters {
		haystack := normalizeVietnamese(strings.ToLower(strings.Join([]string{
			letter.ContractCode,
			letter.ContractName,
			letter.LetterNo,
			letter.LetterType,
			letter.Amount,
			letter.Status,
			letter.OriginalNo,
			letter.RevisionNo,
			letter.Note,
		}, " ")))
		if strings.Contains(haystack, keyword) {
			matches = append(matches, letter)
		}
	}
	return matches
}

func extractLookupKeyword(query string) string {
	replacer := strings.NewReplacer(
		"vy", "",
		"thu", "",
		"hop dong", "",
		"kiem tra", "",
		"tra cuu", "",
		"tracuu", "",
		"tim", "",
		"giup", "",
		"xem", "",
		"thu nao", "",
		"nhung thu", "",
		"nao", "",
		"gan het han", "",
		"sap het han", "",
		"con han bao nhieu", "",
		"het han", "",
	)
	return strings.TrimSpace(replacer.Replace(query))
}

func isActiveLetter(status string) bool {
	s := normalizeVietnamese(strings.ToLower(strings.TrimSpace(status)))
	if s == "" {
		return true
	}
	inactive := []string{"da tu chinh", "da gia han", "da giai toa", "da tat toan", "tat toan", "huy", "da huy", "het hieu luc", "dong"}
	for _, item := range inactive {
		if strings.Contains(s, item) {
			return false
		}
	}
	return true
}

func nextRunTime(raw string, loc *time.Location) time.Time {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	hour, minute := 8, 0
	if len(parts) >= 2 {
		if parsed, err := strconv.Atoi(parts[0]); err == nil && parsed >= 0 && parsed <= 23 {
			hour = parsed
		}
		if parsed, err := strconv.Atoi(parts[1]); err == nil && parsed >= 0 && parsed <= 59 {
			minute = parsed
		}
	}

	now := time.Now().In(loc)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func todayIn(loc *time.Location) time.Time {
	return todayInTime(time.Now().In(loc), loc)
}

func todayInTime(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func getenv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func normalizeVietnamese(value string) string {
	replacer := strings.NewReplacer(
		"à", "a", "á", "a", "ạ", "a", "ả", "a", "ã", "a", "â", "a", "ầ", "a", "ấ", "a", "ậ", "a", "ẩ", "a", "ẫ", "a", "ă", "a", "ằ", "a", "ắ", "a", "ặ", "a", "ẳ", "a", "ẵ", "a",
		"è", "e", "é", "e", "ẹ", "e", "ẻ", "e", "ẽ", "e", "ê", "e", "ề", "e", "ế", "e", "ệ", "e", "ể", "e", "ễ", "e",
		"ì", "i", "í", "i", "ị", "i", "ỉ", "i", "ĩ", "i",
		"ò", "o", "ó", "o", "ọ", "o", "ỏ", "o", "õ", "o", "ô", "o", "ồ", "o", "ố", "o", "ộ", "o", "ổ", "o", "ỗ", "o", "ơ", "o", "ờ", "o", "ớ", "o", "ợ", "o", "ở", "o", "ỡ", "o",
		"ù", "u", "ú", "u", "ụ", "u", "ủ", "u", "ũ", "u", "ư", "u", "ừ", "u", "ứ", "u", "ự", "u", "ử", "u", "ữ", "u",
		"ỳ", "y", "ý", "y", "ỵ", "y", "ỷ", "y", "ỹ", "y",
		"đ", "d",
	)
	return replacer.Replace(value)
}
