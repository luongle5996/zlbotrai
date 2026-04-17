# Bước 1: Build mã nguồn Go
FROM golang:1.22-alpine AS builder

# Cài đặt các thư viện cần thiết cho hệ thống
RUN apk add --no-cache git gcc musl-dev

# Thiết lập thư mục làm việc
WORKDIR /app

# Copy các tệp cấu hình thư viện
COPY go.mod go.sum ./
RUN go mod download

# Copy toàn bộ mã nguồn
COPY . .

# Build ứng dụng (Build cả main và các file hỗ trợ trong thư mục bot)
RUN go build -o zalo-bot ./bot/main.go ./bot/ai.go ./bot/search.go ./bot/db.go

# Bước 2: Tạo môi trường chạy gọn nhẹ
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Ho_Chi_Minh

WORKDIR /app

# Copy file thực thi từ bước build
COPY --from=builder /app/zalo-bot .

# Render cung cấp cổng qua biến môi trường PORT, mặc định là 8080
EXPOSE 8080

# Chạy ứng dụng
CMD ["./zalo-bot"]
