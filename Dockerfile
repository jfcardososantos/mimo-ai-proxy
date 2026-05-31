#
# File: Dockerfile
# Project: mimoproxy
# Purpose: Container definition
# Created: 2026-04-28
#

# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mimoproxy main.go

# Final stage
FROM alpine:latest

ARG SERVICE_TOKEN
ARG USER_ID
ARG XIAOMI_CHATBOT_PH
ARG API_KEY
ARG CORS_ORIGIN
ARG PORT=3000

# Install CA certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

ENV SERVICE_TOKEN=$SERVICE_TOKEN \
    USER_ID=$USER_ID \
    XIAOMI_CHATBOT_PH=$XIAOMI_CHATBOT_PH \
    API_KEY=$API_KEY \
    CORS_ORIGIN=$CORS_ORIGIN \
    PORT=$PORT

# Create data directory for SQLite
RUN mkdir -p /app/data && chmod 777 /app/data

# Copy the binary from the builder stage
COPY --from=builder /app/mimoproxy .
# Copy templates for the dashboard
COPY --from=builder /app/templates ./templates

EXPOSE 3000

CMD ["./mimoproxy"]
