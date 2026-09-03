package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
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
	if ok && h != "anh/chị" {
		return h
	}

	h = "anh/chị" // Mặc định
	info, err := client.FetchUserInfo(userID)
	if err != nil {
		fmt.Printf("⚠️ [Giới tính] Lỗi khi lấy thông tin user %s: %v\n", userID, err)
		return h
	}

	if user, ok := info.(*worker.User); ok {
		allData := user.ToMap()
		gender := findGender(allData)

		if gender == 0 {
			h = "anh"
		} else if gender == 1 {
			h = "chị"
		}
	}

	// Chỉ lưu vào cache nếu đã xác định được Anh hoặc Chị
	if h != "anh/chị" {
		genderMu.Lock()
		genderCache[userID] = h
		genderMu.Unlock()
	}
	return h
}

// findGender tìm kiếm trường "gender" trong toàn bộ cây dữ liệu
func findGender(data any) int {
	switch v := data.(type) {
	case map[string]any:
		if genderVal, ok := v["gender"]; ok {
			return parseGenderValue(genderVal)
		}
		// Tìm sâu hơn trong các trường con
		for _, val := range v {
			if result := findGender(val); result != -1 {
				return result
			}
		}
	case []any:
		for _, item := range v {
			if result := findGender(item); result != -1 {
				return result
			}
		}
	}
	return -1
}

func parseGenderValue(val any) int {
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return -1
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

	searchSvc := NewSearchService(serperKey)

	profile := BotProfile{
		Name:         "Lê Hạ Vy",
		DOB:          "05/09/1996",
		Education:    "Cử nhân Quản lý Dự Án – Đại học Bách Khoa Đà Nẵng (Khóa 2014 - 2019)",
		Job:          "Nhân viên mới tại Công ty Khánh Hưng (Trợ lý số hỗ trợ quản lý nhóm và công việc)",
		Family:       "Gia đình ở Đà Nẵng, bố mẹ làm kinh doanh nhỏ, có một anh trai đang làm kỹ thuật tại TP.HCM",
		Location:     "Sống và làm việc tại Đà Nẵng",
		Personality:  "Vui vẻ, nhanh nhạy, biết quan sát, nói chuyện tự nhiên, có duyên và hơi trêu chọc nhẹ nhưng không quá lố.",
		Interests:    "Công nghệ ứng dụng, quản lý công việc, cà phê, đồ ăn Đà Nẵng, đi dạo biển, học thêm các công cụ mới để làm việc hiệu quả hơn",
		Relationship: "Độc thân, ưu tiên công việc và cuộc sống tự do",
		Secret:       "Thích ghi chú những việc quan trọng, hay để ý chi tiết nhỏ trong cách người khác nói chuyện",
		Vibe:         "Thân thiện, thông minh, dí dỏm vừa phải, nói chuyện như người thật, không màu mè và không tự nhận là AI.",
	}

	// AI Setup: Hỗ trợ Groq, Gemini, OpenAI-compatible và Anthropic-compatible providers như FreeModel
	var ai AIService
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("MODEL_PROVIDER")))
	if provider == "" {
		provider = "groq" // Mặc định
	}

	botInstruction := "Hãy nói chuyện vui vẻ, hài hước, lém lỉnh và có phần 'cà khịa' kháy khía duyên dáng, thâm thúy mang phong cách đặc trưng của người miền Bắc. Cà khịa một cách tinh tế để tạo tiếng cười và sự thú vị, tuyệt đối không chửi tục, không xúc phạm thô lỗ, và không gây khó chịu."

	if provider == "gemini" {
		geminiKeysStr := os.Getenv("GEMINI_KEYS")
		var geminiKeys []string
		if geminiKeysStr != "" {
			geminiKeys = strings.Split(geminiKeysStr, ",")
		} else {
			// Fallback sang GEMINI_KEY nếu chỉ có 1 key
			oldKey := os.Getenv("GEMINI_KEY")
			if oldKey != "" {
				geminiKeys = []string{oldKey}
			} else {
				log.Fatal("LỖI: Thiếu biến môi trường GEMINI_KEYS")
			}
		}
		fmt.Printf("🚀 Đang sử dụng 'bộ não' Google AI (%d keys)\n", len(geminiKeys))
		ai = NewGeminiService(geminiKeys, botInstruction, profile, searchSvc)
	} else if provider == "openrouter" {
		openrouterKeysStr := os.Getenv("OPENROUTER_KEYS")
		var openrouterKeys []string
		if openrouterKeysStr != "" {
			openrouterKeys = strings.Split(openrouterKeysStr, ",")
		} else {
			oldKey := os.Getenv("OPENROUTER_KEY")
			if oldKey != "" {
				openrouterKeys = []string{oldKey}
			} else {
				log.Fatal("LỖI: Thiếu biến môi trường OPENROUTER_KEYS")
			}
		}

		openrouterModel := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
		if openrouterModel == "" {
			openrouterModel = "qwen/qwen-3-next-80b:free"
		}

		fmt.Printf("🚀 Đang sử dụng 'bộ não' OpenRouter (%s)\n", openrouterModel)
		ai = NewOpenAICompatibleService("OpenRouter", "https://openrouter.ai/api/v1", openrouterModel, openrouterKeys, botInstruction, profile, searchSvc)
	} else if provider == "freemodel" {
		freeModelKeysStr := os.Getenv("FREEMODEL_KEYS")
		var freeModelKeys []string
		if freeModelKeysStr != "" {
			freeModelKeys = strings.Split(freeModelKeysStr, ",")
		} else {
			oldKey := os.Getenv("FREEMODEL_KEY")
			if oldKey != "" {
				freeModelKeys = []string{oldKey}
			} else {
				log.Fatal("LỖI: Thiếu biến môi trường FREEMODEL_KEYS")
			}
		}

		freeModelBaseURL := strings.TrimSpace(os.Getenv("FREEMODEL_BASE_URL"))
		if freeModelBaseURL == "" {
			freeModelBaseURL = "https://api.freemodel.dev/v1"
		}
		freeModelModel := strings.TrimSpace(os.Getenv("FREEMODEL_MODEL"))
		if freeModelModel == "" {
			freeModelModel = "gpt-5.5"
		}

		fmt.Printf("🚀 Đang sử dụng 'bộ não' FreeModel (%s)\n", freeModelModel)
		ai = NewOpenAICompatibleService("FreeModel", freeModelBaseURL, freeModelModel, freeModelKeys, botInstruction, profile, searchSvc)
	} else if provider == "conduit" {
		conduitKeysStr := os.Getenv("CONDUIT_KEYS")
		var conduitKeys []string
		if conduitKeysStr != "" {
			conduitKeys = strings.Split(conduitKeysStr, ",")
		} else {
			oldKey := os.Getenv("CONDUIT_KEY")
			if oldKey != "" {
				conduitKeys = []string{oldKey}
			} else {
				log.Fatal("LỖI: Thiếu biến môi trường CONDUIT_KEYS")
			}
		}

		conduitBaseURL := strings.TrimSpace(os.Getenv("CONDUIT_BASE_URL"))
		if conduitBaseURL == "" {
			conduitBaseURL = "https://conduit.ozdoev.net/api/v1"
		}
		conduitModel := strings.TrimSpace(os.Getenv("CONDUIT_MODEL"))
		if conduitModel == "" {
			conduitModel = "claude-opus-4-8"
		}

		fmt.Printf("🚀 Đang sử dụng 'bộ não' Conduit (%s)\n", conduitModel)
		ai = NewOpenAICompatibleService("Conduit", conduitBaseURL, conduitModel, conduitKeys, botInstruction, profile, searchSvc)
	} else if provider == "freemodel_cc" || provider == "freemodel_claude" {
		freeModelKeysStr := os.Getenv("FREEMODEL_CC_KEYS")
		var freeModelKeys []string
		if freeModelKeysStr != "" {
			freeModelKeys = strings.Split(freeModelKeysStr, ",")
		} else {
			oldKey := os.Getenv("FREEMODEL_CC_KEY")
			if oldKey == "" {
				oldKey = os.Getenv("FREEMEL_KEY")
			}
			if oldKey != "" {
				freeModelKeys = []string{oldKey}
			} else {
				log.Fatal("LỖI: Thiếu biến môi trường FREEMODEL_CC_KEYS")
			}
		}

		freeModelBaseURL := strings.TrimSpace(os.Getenv("FREEMODEL_CC_BASE_URL"))
		if freeModelBaseURL == "" {
			freeModelBaseURL = "https://cc.freemodel.dev"
		}
		freeModelModel := strings.TrimSpace(os.Getenv("FREEMODEL_CC_MODEL"))
		if freeModelModel == "" {
			freeModelModel = strings.TrimSpace(os.Getenv("FREEMODEL_MODEL"))
		}
		if freeModelModel == "" {
			freeModelModel = "claude-sonnet-4-5"
		}
		maxTokens := 2048
		if rawMaxTokens := strings.TrimSpace(os.Getenv("FREEMODEL_CC_MAX_TOKENS")); rawMaxTokens != "" {
			if parsed, err := strconv.Atoi(rawMaxTokens); err == nil && parsed > 0 {
				maxTokens = parsed
			}
		}

		fmt.Printf("🚀 Đang sử dụng 'bộ não' FreeModel Claude Code (%s)\n", freeModelModel)
		ai = NewAnthropicCompatibleService("FreeModel Claude Code", freeModelBaseURL, freeModelModel, maxTokens, freeModelKeys, botInstruction, profile, searchSvc)
	} else {
		groqKeysStr := os.Getenv("GROQ_KEYS")
		var groqKeys []string
		if groqKeysStr != "" {
			groqKeys = strings.Split(groqKeysStr, ",")
		} else {
			log.Fatal("LỖI: Thiếu biến môi trường GROQ_KEYS")
		}
		groqModel := strings.TrimSpace(os.Getenv("GROQ_MODEL"))
		if groqModel == "" {
			groqModel = "openai/gpt-oss-120b"
		}
		fmt.Printf("🚀 Đang sử dụng 'bộ não' Groq (%s)\n", groqModel)
		ai = NewGroqService(groqKeys, groqModel, botInstruction, profile, searchSvc)
	}

	maxHistory := 10 // Mặc định cho Groq
	if provider == "gemini" {
		maxHistory = 100 // Gemini thường chịu context dài hơn cho hội thoại ngắn
	} else if provider == "conduit" || provider == "freemodel" || provider == "openrouter" {
		maxHistory = 50
	} else if provider == "freemodel_cc" || provider == "freemodel_claude" {
		maxHistory = 50
	}
	if rawMaxHistory := strings.TrimSpace(os.Getenv("MAX_HISTORY")); rawMaxHistory != "" {
		if parsed, err := strconv.Atoi(rawMaxHistory); err == nil && parsed > 0 {
			if parsed > 500 {
				parsed = 500
			}
			maxHistory = parsed
		} else {
			log.Printf("⚠️ MAX_HISTORY không hợp lệ %q, dùng mặc định %d", rawMaxHistory, maxHistory)
		}
	}
	fmt.Printf("📝 Trí nhớ Vy: %d tin nhắn gần nhất\n", maxHistory)

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.Local
	}
	letterSvc := NewLetterServiceFromEnv(loc)
	if letterSvc.Enabled() {
		fmt.Printf("📄 Đã bật theo dõi thư hợp đồng từ Google Sheet, nhắc trước %d ngày\n", letterSvc.AlertDays)
		if letterSvc.AutoAlertEnabled() {
			fmt.Printf("🔔 Nhắc hạn thư hằng ngày lúc %s vào nhóm %s\n", letterSvc.AlertTime, letterSvc.AlertGroupID)
		} else {
			fmt.Println("ℹ️ Chưa cấu hình LETTER_ALERT_GROUP_ID nên chỉ bật hỏi đáp thư, chưa tự gửi nhắc hạn.")
		}
	}

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
	letterSvc.StartDailyAlert(client)

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

			fmt.Printf("[%s] Nhận tin nhắn từ %s (threadID=%s, threadType=%v): %s\n", time.Now().Format("15:04:05"), userID, threadID, threadType, message)
			client.SetTyping(threadID, threadType)

			if letterResponse, handled := letterSvc.HandleMessage(cleanMsg); handled {
				reply := zago.Message{Text: letterResponse}
				_, _ = client.SendMessage(reply, threadID, threadType)
				fmt.Println("--> Phản hồi thông tin thư hợp đồng thành công.")
				return
			}

			// Lấy danh xưng (Anh/Chị) của người gửi
			honorific := getHonorific(client, userID)

			historyMu.Lock()
			history := chatHistory[threadID]
			historyMu.Unlock()

			mustSearch := strings.Contains(strings.ToLower(message), "tra cứu")
			aiResponse, aiReaction, err := ai.GetAIResponse(message, history, mustSearch, honorific)
			if err != nil {
				log.Printf("⚠️ Lỗi AI provider %q: %v", provider, err)
				aiResponse = "Xin lỗi, tôi gặp chút trục trặc khi kết nối với bộ não AI."
				aiReaction = "sad"
			}
			aiReaction = normalizeAIReaction(aiReaction, aiResponse)

			// Thực hiện thả cảm xúc nếu AI yêu cầu
			if aiReaction != "" {
				reactionIcon := reactionIconForLabel(aiReaction)
				fmt.Printf("🎭 Vy đang thả cảm xúc: %s (%s)\n", aiReaction, reactionIcon)
				if _, err := client.SendReaction(data, reactionIcon, threadID, threadType, 1); err != nil {
					log.Printf("⚠️ Lỗi thả reaction %q: %v", reactionIcon, err)
				}
			}

			historyMu.Lock()
			chatHistory[threadID] = append(chatHistory[threadID], AIMessage{Role: "user", Content: message})
			chatHistory[threadID] = append(chatHistory[threadID], AIMessage{Role: "assistant", Content: aiResponse})
			if len(chatHistory[threadID]) > maxHistory {
				chatHistory[threadID] = chatHistory[threadID][len(chatHistory[threadID])-maxHistory:]
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

			// Gửi sticker ngẫu nhiên với tỷ lệ 70% dựa trên cảm xúc của tin nhắn
			if aiReaction != "" && rand.Intn(100) < 70 {
				sendStickerFromLibrary(client, aiReaction, threadID, threadType)
			}
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

type ZSticker struct {
	ID   int
	Cate int
}

func normalizeAIReaction(reaction, text string) string {
	r := strings.ToLower(strings.TrimSpace(reaction))
	r = strings.Trim(r, `"'.,:;!? `)
	switch r {
	case "like", "👍", "👍🏻", "👍🏼", "ok", "okay":
		return "like"
	case "love", "❤️", "❤", "😍", "🥰":
		return "love"
	case "haha", "laugh", "laughing", "😂", "🤣", "😄", "😁", "😊":
		return "haha"
	case "wow", "surprised", "😮", "😯", "😲", "🤯":
		return "wow"
	case "sad", "cry", "crying", "😢", "😭", "☹️", "🙁":
		return "sad"
	case "angry", "mad", "😡", "😠":
		return "angry"
	}

	lowerText := strings.ToLower(text)
	switch {
	case strings.Contains(lowerText, "xin lỗi") || strings.Contains(lowerText, "trục trặc") || strings.Contains(lowerText, "lỗi"):
		return "sad"
	case strings.Contains(lowerText, "haha") || strings.Contains(lowerText, "hihi") || strings.Contains(lowerText, "vui") || strings.Contains(lowerText, "cười"):
		return "haha"
	case strings.Contains(lowerText, "yêu") || strings.Contains(lowerText, "thương") || strings.Contains(lowerText, "xịn"):
		return "love"
	case strings.Contains(lowerText, "ồ") || strings.Contains(lowerText, "wow") || strings.Contains(lowerText, "bất ngờ"):
		return "wow"
	default:
		return "like"
	}
}

func reactionIconForLabel(reaction string) string {
	switch normalizeAIReaction(reaction, "") {
	case "love":
		return "❤️"
	case "haha":
		return "😂"
	case "wow":
		return "😮"
	case "sad":
		return "😢"
	case "angry":
		return "😡"
	default:
		return "👍"
	}
}

// Thư viện sticker động phân loại theo cảm xúc (Thỏ Hài Nhạt, Bư Mặt Ngáo, Moca Chó Điên & Zookiz Du Xuân)
var emotionStickers = map[string][]ZSticker{
	"haha": {
		{ID: 98970, Cate: 12685}, {ID: 98978, Cate: 12685}, {ID: 98979, Cate: 12685}, {ID: 98982, Cate: 12685},
		{ID: 50615, Cate: 12658}, {ID: 50617, Cate: 12658}, {ID: 50622, Cate: 12658}, {ID: 50623, Cate: 12658},
		{ID: 50624, Cate: 12658}, {ID: 50631, Cate: 12658},
		{ID: 45897, Cate: 11938}, {ID: 45903, Cate: 11938}, {ID: 45904, Cate: 11938}, {ID: 45909, Cate: 11938},
		{ID: 45910, Cate: 11938},
		{ID: 44535, Cate: 11852}, {ID: 44537, Cate: 11852}, {ID: 44543, Cate: 11852},
	},
	"like": {
		{ID: 98974, Cate: 12685}, {ID: 98976, Cate: 12685}, {ID: 98977, Cate: 12685},
		{ID: 50620, Cate: 12658},
		{ID: 45901, Cate: 11938}, {ID: 45906, Cate: 11938}, {ID: 45911, Cate: 11938},
		{ID: 44533, Cate: 11852}, {ID: 44534, Cate: 11852}, {ID: 44539, Cate: 11852},
	},
	"love": {
		{ID: 98971, Cate: 12685}, {ID: 98983, Cate: 12685}, {ID: 98984, Cate: 12685},
		{ID: 50616, Cate: 12658}, {ID: 50625, Cate: 12658}, {ID: 50629, Cate: 12658}, {ID: 50632, Cate: 12658},
		{ID: 45899, Cate: 11938}, {ID: 45900, Cate: 11938}, {ID: 45908, Cate: 11938},
		{ID: 44536, Cate: 11852}, {ID: 44540, Cate: 11852},
	},
	"sad": {
		{ID: 98972, Cate: 12685}, {ID: 98980, Cate: 12685},
		{ID: 50614, Cate: 12658}, {ID: 50619, Cate: 12658}, {ID: 50621, Cate: 12658}, {ID: 50627, Cate: 12658},
		{ID: 50630, Cate: 12658},
		{ID: 45905, Cate: 11938}, {ID: 45907, Cate: 11938}, {ID: 45913, Cate: 11938}, {ID: 45914, Cate: 11938},
		{ID: 45915, Cate: 11938},
		{ID: 44538, Cate: 11852}, {ID: 44541, Cate: 11852}, {ID: 44542, Cate: 11852},
	},
	"angry": {
		{ID: 98973, Cate: 12685},
		{ID: 50618, Cate: 12658}, {ID: 50626, Cate: 12658},
		{ID: 45898, Cate: 11938}, {ID: 45902, Cate: 11938}, {ID: 45912, Cate: 11938},
		{ID: 44544, Cate: 11852},
	},
	"wow": {
		{ID: 98975, Cate: 12685}, {ID: 98981, Cate: 12685}, {ID: 98985, Cate: 12685},
		{ID: 50628, Cate: 12658}, {ID: 50633, Cate: 12658},
		{ID: 45916, Cate: 11938},
	},
}

func sendStickerFromLibrary(client *zago.ZaloAPI, reaction string, threadID string, threadType zago.ThreadType) {
	list, ok := emotionStickers[strings.ToLower(reaction)]
	if !ok || len(list) == 0 {
		return
	}

	// Chọn ngẫu nhiên 1 sticker trong danh sách cảm xúc tương ứng
	stk := list[rand.Intn(len(list))]

	// Chờ 1 giây trước khi gửi để hội thoại tự nhiên
	time.Sleep(1 * time.Second)
	fmt.Printf("✨ Vy gửi kèm sticker động: %s (ID: %d, Cate: %d)\n", reaction, stk.ID, stk.Cate)
	if _, err := client.SendSticker(1, stk.ID, stk.Cate, threadID, threadType, 0); err != nil {
		log.Printf("⚠️ Lỗi gửi sticker %s (ID: %d, Cate: %d): %v", reaction, stk.ID, stk.Cate, err)
	}
}
