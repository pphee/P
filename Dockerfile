# Build stage
FROM golang:1.22.2 AS builder
WORKDIR /app
COPY . .
RUN go mod tidy
RUN go vet ./...
RUN gofmt -w .
RUN CGO_ENABLED=0 GOOS=linux go build -o main .

# Final stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/main .
CMD ["./main"]

