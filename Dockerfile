FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o app main.go filter.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/app .
COPY filters/ ./filters/
ENTRYPOINT ["./app"]
