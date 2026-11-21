package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNotionDateAndGroupedAlert(t *testing.T) {
	date := notionDate(json.RawMessage(`{"type":"date","date":{"start":"2026-11-20","end":null}}`))
	if date != "20/11/2026" {
		t.Fatalf("unexpected sent date: %q", date)
	}
	message := formatNewDossiers([]dossierRecord{
		{Name: "KTBH HĐ 32", SentDate: date, Status: "Đã gửi công trình", Recipient: "Anh Duy", Action: "Ký, trình Giám đốc Ban rồi gửi về công ty"},
		{Name: "Biên bản nghiệm thu", SentDate: date, Status: "Đã gửi công trình"},
	}, time.Date(2026, 11, 20, 9, 15, 0, 0, time.FixedZone("ICT", 7*60*60)))
	for _, part := range []string{"2 hồ sơ mới", "09:15 20/11/2026", "1. 20/11/2026 — KTBH HĐ 32", "2. 20/11/2026 — Biên bản nghiệm thu", "Người nhận tại công trình: Anh Duy", "Cần làm: Ký, trình Giám đốc Ban rồi gửi về công ty"} {
		if !strings.Contains(message, part) {
			t.Errorf("alert missing %q: %s", part, message)
		}
	}
	if strings.Count(message, dossierTableURL) != 1 {
		t.Fatalf("expected one table link: %s", message)
	}
}

func TestDigestCutoffVietnamTime(t *testing.T) {
	loc := time.FixedZone("ICT", 7*60*60)
	for _, test := range []struct {
		hour, minute int
		due          bool
	}{{17, 59, false}, {18, 0, true}, {19, 0, true}} {
		at := time.Date(2026, 11, 20, test.hour, test.minute, 0, 0, loc)
		cutoff, due := digestCutoff(at, "18:00")
		if due != test.due || cutoff.In(loc).Hour() != 18 {
			t.Errorf("at %s: cutoff=%s due=%v", at, cutoff, due)
		}
	}
}

func TestGroupedUpdateAfterQuietPeriod(t *testing.T) {
	now := time.Date(2026, 11, 21, 10, 15, 0, 0, time.UTC)
	previousRecord := dossierRecord{Status: "Đã gửi công trình"}
	previous := dossierSnapshot{NewAnnounced: true, Signature: dossierSignature(previousRecord)}
	record := dossierRecord{Status: "Đang ký", SignedBy: "Anh Duy", NextSign: "Anh Pháp", LastEditedAt: now.Add(-30 * time.Second)}
	if readyForUpdate(record, previous, now, time.Minute) {
		t.Fatal("sent before editing settled")
	}
	record.LastEditedAt = now.Add(-time.Minute)
	if !readyForUpdate(record, previous, now, time.Minute) {
		t.Fatal("did not send settled update")
	}
	message := formatDossierAlert(dossierRecord{Name: "KTBH HĐ 32", SentDate: "20/11/2026", Status: "Đang ký", SignedBy: "Anh Duy", NextSign: "Anh Pháp"}, "Đang ký", false)
	for _, part := range []string{"Cập nhật hồ sơ", "Đã ký: Anh Duy", "Chờ ký: Anh Pháp", "Trạng thái: Đang ký"} {
		if !strings.Contains(message, part) {
			t.Errorf("missing %q: %s", part, message)
		}
	}
}

func TestUpdateWaitSetting(t *testing.T) {
	if got := updateWaitFromEnv(""); got != time.Minute {
		t.Fatalf("default wait = %s", got)
	}
	if got := updateWaitFromEnv("5"); got != 5*time.Minute {
		t.Fatalf("configured wait = %s", got)
	}
}

func TestThreeDossierUpdatesInOneMessage(t *testing.T) {
	changes := []dossierChange{
		{Record: dossierRecord{Name: "Hồ sơ A", Status: "Đang ký", SignedBy: "Anh Duy"}, PreviousStatus: "Đã gửi công trình"},
		{Record: dossierRecord{Name: "Hồ sơ B", Status: "Đã ký", NextSign: "Chị Tiền"}, PreviousStatus: "Đang ký"},
		{Record: dossierRecord{Name: "Hồ sơ C", Status: "Đang gửi về", Holder: "Chị Tiền"}, PreviousStatus: "Đã ký"},
	}
	message := formatUpdatedDossiers(changes, time.Date(2026, 11, 21, 10, 0, 0, 0, time.FixedZone("ICT", 7*60*60)))
	for _, part := range []string{"3 hồ sơ cập nhật", "1. Hồ sơ A", "2. Hồ sơ B", "3. Hồ sơ C", "Đã ký: Anh Duy"} {
		if !strings.Contains(message, part) {
			t.Errorf("missing %q: %s", part, message)
		}
	}
	if strings.Count(message, dossierTableURL) != 1 {
		t.Fatalf("expected one table link: %s", message)
	}
}
