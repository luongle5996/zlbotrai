# Zago

`Zago` is a Go library for working with Zalo in a cleaner, modular layout.

## Highlights

- Root package kept small and public-facing
- Internal implementation isolated under `internal/`
- Dedicated documentation website under `documents/`
- Go module based project layout

## Project Structure

```text
Zago/
├── documents/              # VitePress documentation website
├── internal/
│   ├── api/                # grouped API services
│   ├── app/                # session and state
│   ├── auth/               # authentication helpers
│   ├── core/               # shared domain primitives
│   ├── logger/             # logging helpers
│   ├── util/               # common utilities
│   └── worker/             # event/message objects
├── doc.go
├── go.mod
├── socket_callbacks.go
├── types.go
└── zalo.go
```

## Requirements

- Go `1.22+`
- Node.js `20+` for the documentation website

## Development

Compile the Go library:

```bash
go test ./...
```

Even without test files, `go test ./...` is still a convenient compile check for the whole module.

## Theo dõi thư hợp đồng bằng Google Sheet

Bot có thể đọc một Google Sheet dạng CSV để trả lời thông tin thư/hợp đồng và tự nhắc hằng ngày vào một nhóm Zalo cố định.

### Bố cục Google Sheet

Tạo sheet với hàng đầu tiên đúng các tên cột sau:

```text
ma_hop_dong | ten_hop_dong | so_thu | loai_thu | so_tien | ngay_phat_hanh | ngay_het_han | trang_thai | thu_goc | lan_tu_chinh | ghi_chu
```

Ví dụ:

```text
HD001 | Công trình A | BL-001 | Bảo lãnh thực hiện HĐ | 250.000.000 | 01/09/2026 | 01/10/2026 | đã gia hạn |  |  | Đã tu chỉnh bằng BL-001-TC1
HD001 | Công trình A | BL-001-TC1 | Tu chỉnh bảo lãnh thực hiện HĐ | 250.000.000 | 25/09/2026 | 01/12/2026 | còn hiệu lực | BL-001 | 1 | Gia hạn lần 1
HD002 | Công trình B | BL-009 | Bảo lãnh bảo hành | 80.000.000 | 15/08/2026 | 10/09/2026 | đã tất toán |  |  |
```

Ghi chú:

- `ngay_het_han` là cột bắt buộc để bot tính hạn.
- `thu_goc` và `lan_tu_chinh` dùng để xem chi tiết lịch sử gia hạn/tu chỉnh.
- Ngày nên nhập dạng `dd/mm/yyyy`, ví dụ `03/09/2026`.
- Các trạng thái `đã tu chỉnh`, `hết hiệu lực`, `đã giải tỏa` sẽ không được nhắc hạn.
- Dòng nào thiếu cả `ma_hop_dong` và `so_thu` sẽ bị bỏ qua.

### Lấy link CSV từ Google Sheet

Cách đơn giản nhất là dùng sheet có quyền xem bằng link:

1. Vào Google Sheet, bấm `Share`, đặt quyền `Anyone with the link can view`.
2. Lấy `spreadsheetId` trên URL của sheet.
3. Lấy `gid` của tab sheet ở cuối URL.
4. Tạo link CSV theo mẫu:

```text
https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/export?format=csv&gid=GID
```

### Biến môi trường

Thêm các biến này vào Render hoặc file môi trường khi chạy local:

```text
LETTER_SHEET_CSV_URL=https://docs.google.com/spreadsheets/d/SPREADSHEET_ID/export?format=csv&gid=GID
LETTER_ALERT_GROUP_ID=ID_NHOM_ZALO_CO_DINH
LETTER_ALERT_DAYS=30
LETTER_ALERT_TIME=08:00
```

Trong đó:

- `LETTER_SHEET_CSV_URL`: link CSV của Google Sheet.
- `LETTER_ALERT_GROUP_ID`: id nhóm Zalo duy nhất nhận thông báo tự động.
- `LETTER_ALERT_DAYS`: số ngày trước hạn cần nhắc, mặc định `30`.
- `LETTER_ALERT_TIME`: giờ nhắc mỗi ngày theo giờ Việt Nam, mặc định `08:00`.

Nếu chưa đặt `LETTER_ALERT_GROUP_ID`, bot vẫn trả lời lệnh hỏi thư nhưng không tự gửi thông báo hằng ngày.

### Cách hỏi bot

Trong nhóm cần nhắc tên bot như bình thường, ví dụ:

```text
Vy thư gần hết hạn
Vy thư BL-001
Vy hợp đồng HD001
```

Bot chỉ chủ động nhắc hạn vào nhóm cố định trong `LETTER_ALERT_GROUP_ID`. Các tính năng chat khác vẫn hoạt động như trước.

## Documentation Website

The docs site lives in `documents/` and runs on port `14711`.

Install dependencies:

```bash
cd documents
npm install
```

Run locally:

```bash
npm run docs:dev
```

Build static docs:

```bash
npm run docs:build
```

Preview the built site:

```bash
npm run docs:preview
```

## Public API Entry Points

- `Zalo(...)` creates a new client instance
- `SocketCallbacks` wires realtime event handlers
- `Message`, `User`, `Group`, `ThreadType` are re-exported for consumers

## Notes

- The current module path is `github.com/tranhaonguyendev/za-go`
- If you publish this under a different repository path, update `go.mod`
