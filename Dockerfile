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

# Build ứng dụng với chế độ hiển thị chi tiết (-v)
# CGO_ENABLED=0 giúp tạo binary tĩnh hoàn toàn
RUN CGO_ENABLED=0 go build -v -o zalo-bot ./bot/

# Bước 2: Tạo môi trường chạy gọn nhẹ
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Ho_Chi_Minh

WORKDIR /app

# Copy file thực thi từ bước build
COPY --from=builder /app/zalo-bot .

# Render cung cấp cổng qua biến môi trường PORT
EXPOSE 8080

# Chạy ứng dụng
CMD ["./zalo-bot"]
