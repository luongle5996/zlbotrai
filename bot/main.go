package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tranhaonguyendev/za-go"
	"github.com/tranhaonguyendev/za-go/internal/worker"
)

var genderCache = make(map[string]string)
var genderMu sync.RWMutex

func getHonorific(client *zago.ZaloAPI, userID string) string {
	genderMu.RLock()
	h, ok := genderCache[userID]
	genderMu.RUnlock()
	if ok {
		return h
	}

	h = "anh/chị" // Mặc định
	info, err := client.FetchUserInfo(userID)
	if err == nil {
		if user, ok := info.(*worker.User); ok {
			// Thử lấy danh sách profiles trực tiếp (do thư viện đã làm phẳng data)
			profilesObj := user.Get("profiles")
			if profiles, ok := profilesObj.([]any); ok && len(profiles) > 0 {
				if p, ok := profiles[0].(map[string]any); ok {
					if genderVal, ok := p["gender"]; ok {
						// Chuyển đổi linh hoạt các kiểu số khác nhau
						gender := -1
						switch v := genderVal.(type) {
						case float64:
							gender = int(v)
						case int:
							gender = v
						case string:
							gender, _ = strconv.Atoi(v)
						}

						if gender == 0 {
							h = "anh"
						} else if gender == 1 {
							h = "chị"
						}
					}
				}
			}
		}
	}

	genderMu.Lock()
	genderCache[userID] = h
	genderMu.Unlock()
	return h
}

var sessionFile = "session.json"

func main() {
	// 1. Khởi tạo cấu hình từ biến môi trường (Cho Render)
	imei := os.Getenv("ZALO_IMEI")
	if imei == "" {
		imei = "my-static-imei-zago"
	}

	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		log.Fatal("LỖI: Thiếu biến môi trường MONGO_URI")
	}

	serperKey := os.Getenv("SERPER_KEY")
	if serperKey == "" {
		log.Fatal("LỖI: Thiếu biến môi trường SERPER_KEY")
	}

	// AI & DB Setup
	db, err := NewDBService(mongoURI)
	if err != nil {
		fmt.Printf("⚠️ Không thể kết nối MongoDB: %v\n", err)
	} else {
		defer db.Close()
	}

	// Lấy danh sách Groq Keys từ biến môi trường (phân cách bằng dấu phẩy)
	groqKeysStr := os.Getenv("GROQ_KEYS")
	var groqKeys []string
	if groqKeysStr != "" {
		groqKeys = strings.Split(groqKeysStr, ",")
	} else {
		log.Fatal("LỖI: Thiếu biến môi trường GROQ_KEYS")
	}
	searchSvc := NewSearchService(serperKey)

	profile := BotProfile{
		Name:         "Lê Hạ Vy",
		DOB:          "05/09/1996",
		Education:    "Cử nhân Quản lý Dự Án – Đại học Bách Khoa Đà Nẵng (Khóa 2014 - 2019)",
		Job:          "Nhân viên mới tại Công ty Khánh Hưng (Trợ lý số hỗ trợ quản lý nhóm và công việc)",
		Family:       "Con gái thứ hai, bố là giáo viên về hưu, mẹ kinh doanh tạp hóa, anh trai là kỹ sư cầu đường tại TP.HCM",
		Location:     "Căn hộ nhỏ gần đường Lê Đại Hành, Đà Nẵng",
		Personality:  "Nhiệt tình, thông minh, lễ phép, đôi khi hơi 'máy móc' do đặc thù công việc",
		Interests:    "Đồ chơi công nghệ thực dụng (Bàn phím Aula F99), bún chả cá Đà Nẵng, cà phê muối, Python, đi dạo biển",
		Relationship: "Độc thân (vì quá yêu công việc và thích tự do)",
		Secret:       "Có mèo tên 'Data', sợ gián, có thói quen viết sổ tay (bullet journal)",
		Vibe:         "Lễ phép (Dạ/Vâng), nhanh gọn, sử dụng icon 🌸, ✨, 🛠️ hợp lý",
	}

	ai := NewGroqService(groqKeys, "Hãy tập trung hỗ trợ người dùng một cách chuyên nghiệp, lịch sự và khách quan trong mọi lĩnh vực.", profile, searchSvc)
	chatHistory := make(map[string][]AIMessage)
	historyMu := sync.Mutex{}

	// Biến lưu ảnh QR để hiển thị trên trình duyệt
	var currentQR []byte
	var qrMu sync.Mutex

	// Bắt đầu một Web Server nhỏ để Render không bị "ngủ" và để xem mã QR
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "Bot Zalo AI is running! (Time: %s)\nTruy cập /qr để lấy mã đăng nhập.", time.Now().Format(time.RFC822))
		})
		http.HandleFunc("/qr", func(w http.ResponseWriter, r *http.Request) {
			qrMu.Lock()
			data := currentQR
			qrMu.Unlock()
			if data == nil {
				fmt.Fprintf(w, "Chưa có mã QR. Vui lòng đợi hoặc tải lại trang.")
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(data)
		})
		fmt.Printf("📡 Web Server started on port %s. View QR at: /qr\n", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("Lỗi Web Server: %v", err)
		}
	}()

	client, err := zago.Zalo("", "", imei, nil, "", false, zago.LoginAPI)
	if err != nil {
		log.Fatalf("Lỗi khởi tạo client: %v", err)
	}

	// 2. Thử khôi phục phiên đăng nhập
	var cookies map[string]string

	// Ưu tiên 1: Thử từ file cục bộ (nếu chạy local)
	if data, err := os.ReadFile(sessionFile); err == nil {
		_ = json.Unmarshal(data, &cookies)
	}

	// Ưu tiên 2: Thử từ MongoDB (nếu chạy trên Cloud Render)
	if cookies == nil && db != nil {
		fmt.Println("♻️ Đang tìm kiếm phiên đăng nhập trên đám mây (MongoDB)...")
		if cloudCookies, err := db.LoadSession(); err == nil {
			cookies = cloudCookies
		}
	}

	if cookies != nil {
		client.SetSession(cookies)
		if err := client.Login("", "", imei, ""); err == nil && client.IsLoggedIn() {
			fmt.Printf("✅ Đăng nhập thành công từ bộ nhớ: %s\n", client.AccountName())
			goto startListening
		}
		fmt.Println("⚠️ Phiên đăng nhập đã cũ hoặc hết hạn.")
	}

	// 3. Quy trình đăng nhập bằng mã QR (nếu không khôi phục được)
	fmt.Println("=== Đang bắt đầu quy trình đăng nhập bằng mã QR ===")
	{
		qr, err := client.AuthQRCode()
		if err != nil {
			log.Fatalf("Lỗi lấy mã QR: %v", err)
		}

		// Lưu ảnh QR vào bộ nhớ để hiển thị qua Web
		qrMu.Lock()
		currentQR = qr.ImageBytes
		qrMu.Unlock()

		fmt.Printf("\nBƯỚC 1: Đã lấy mã QR mới.\n")
		fmt.Printf("BƯỚC 2: Bạn hãy mở link của Render thêm đuôi /qr để quét mã. Ví dụ: https://ten-cua-ban.onrender.com/qr\n")
		fmt.Println("BƯỚC 3: Dùng ứng dụng Zalo trên điện thoại quét và nhấn 'Đăng nhập'.")

		scanned, err := client.WaitQRCodeScan(qr, 30, 5*time.Second)
		if err != nil || !scanned {
			log.Fatalf("Lỗi hoặc hết thời gian chờ quét mã.")
		}
		fmt.Println("✅ Đã quét mã QR! Vui lòng xác nhận trên điện thoại.")

		cookies, err = client.WaitQRCodeConfirm(qr, 30, 5*time.Second)
		if err != nil || cookies == nil {
			log.Fatalf("Lỗi hoặc hết thời gian chờ xác nhận.")
		}

		client.SetSession(cookies)
		if err := client.Login("", "", imei, ""); err != nil {
			log.Fatalf("Lỗi đồng bộ phiên: %v", err)
		}

		// Lưu lại vào cả file và MongoDB
		if cookieData, err := json.Marshal(cookies); err == nil {
			_ = os.WriteFile(sessionFile, cookieData, 0644)
		}
		if db != nil {
			if err := db.SaveSession(cookies); err == nil {
				fmt.Println("💾 Đã lưu phiên đăng nhập bền vững lên MongoDB Atlas.")
			}
		}
	}

startListening:
	fmt.Printf("🎉 Bot đang hoạt động với tên: %s\n", client.AccountName())

	client.SetSocketCallbacks(zago.SocketCallbacks{
		Message: func(mid, userID, message string, data *worker.MessageObject, threadID string, threadType zago.ThreadType) {
			if userID == client.UserID() {
				return
			}

			// Bỏ qua các tin nhắn rỗng (thường là reaction, sticker đơn thuần hoặc thông báo hệ thống)
			cleanMsg := strings.TrimSpace(message)
			if cleanMsg == "" {
				return
			}

			// Kiểm tra điều kiện nhắc tên
			botName := client.AccountName()
			isMentioned := strings.Contains(strings.ToLower(cleanMsg), strings.ToLower(botName)) || 
						  strings.Contains(strings.ToLower(cleanMsg), "vy")

			// Trong Nhóm: Bắt buộc phải nhắc tên mới trả lời
			// Chat riêng: Trả lời luôn, không cần nhắc tên
			if threadType == zago.ThreadTypeGROUP && !isMentioned {
				return
			}

			fmt.Printf("[%s] Nhận tin nhắn từ %s: %s\n", time.Now().Format("15:04:05"), userID, message)
			client.SetTyping(threadID, threadType)

			// Lấy danh xưng (Anh/Chị) của người gửi
			honorific := getHonorific(client, userID)

			historyMu.Lock()
			history := chatHistory[threadID]
			historyMu.Unlock()

			mustSearch := strings.Contains(strings.ToLower(message), "tra cứu")
			aiResponse, aiReaction, err := ai.GetAIResponse(message, history, mustSearch, honorific)
			if err != nil {
				aiResponse = "Xin lỗi, tôi gặp chút trục trặc khi kết nối với bộ não AI."
			}

			// Thực hiện thả cảm xúc nếu AI yêu cầu
			if aiReaction != "" {
				fmt.Printf("🎭 Vy đang thả cảm xúc: %s\n", aiReaction)
				_, _ = client.SendReaction(data, aiReaction, threadID, threadType, 1)
			}

			historyMu.Lock()
			chatHistory[threadID] = append(chatHistory[threadID], AIMessage{Role: "user", Content: message})
			chatHistory[threadID] = append(chatHistory[threadID], AIMessage{Role: "assistant", Content: aiResponse})
			if len(chatHistory[threadID]) > 10 {
				chatHistory[threadID] = chatHistory[threadID][len(chatHistory[threadID])-10:]
			}
			historyMu.Unlock()
			
			// Giả lập thời gian đánh máy dựa trên độ dài tin nhắn
			// Tốc độ đánh máy trung bình: ~25 ký tự/giây
			charCount := len(aiResponse)
			typingSpeed := 15 + rand.Intn(15) // Tốc độ từ 15-30 ký tự mỗi giây
			
			delay := charCount / typingSpeed
			if delay < 2 {
				delay = 2 // Chờ ít nhất 2 giây
			}
			if delay > 12 {
				delay = 12 // Chờ tối đa 12 giây để không quá lâu
			}
			
			fmt.Printf("... Tin nhắn dài %d ký tự. Đang giả lập đánh máy trong %d giây\n", charCount, delay)
			time.Sleep(time.Duration(delay) * time.Second)

			reply := zago.Message{Text: aiResponse}
			_, _ = client.SendMessage(reply, threadID, threadType)
			fmt.Println("--> Phản hồi thành công.")
		},
		Error: func(err error, ts int64) {
			if err != nil {
				log.Printf("⚠️ Lỗi Socket: %v", err)
			}
		},
	})

	fmt.Println("🚀 Bot AI đang lắng nghe tin nhắn...")
	if err := client.Listen(true, 3); err != nil {
		log.Fatalf("Lỗi khi lắng nghe: %v", err)
	}
	select {}
}
