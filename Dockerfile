FROM golang:1.25-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y git gcc libc6-dev ca-certificates

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Generate swagger docs
RUN go install github.com/swaggo/swag/cmd/swag@latest && \
    mkdir -p cmd/dashboard/admin-dist cmd/dashboard/user-dist

# Download frontend resources
RUN apt-get update && apt-get install -y unzip wget && \
    # Admin Frontend
    wget -qO admin-dist.zip https://github.com/nezhahq/admin-frontend/releases/latest/download/dist.zip && \
    unzip -q admin-dist.zip -d cmd/dashboard/ && \
    mv cmd/dashboard/dist/* cmd/dashboard/admin-dist/ && \
    rm -rf cmd/dashboard/dist admin-dist.zip && \
    # User Frontend
    wget -qO user-dist.zip https://github.com/hamster1963/nezha-dash-v1/releases/latest/download/dist.zip && \
    unzip -q user-dist.zip -d cmd/dashboard/ && \
    mv cmd/dashboard/dist/* cmd/dashboard/user-dist/ && \
    rm -rf cmd/dashboard/dist user-dist.zip && \
    # IPInfo GeoIP
    wget -qO pkg/geoip/geoip.db https://github.com/SagerNet/sing-geoip/releases/latest/download/geoip.db

# Generate swagger (after frontend assets are in place, though swag doesn't strictly depend on them but main.go might embed them)
RUN swag init -g cmd/dashboard/main.go -o cmd/dashboard/docs --parseDependency || true

# Build the application
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o dashboard ./cmd/dashboard

# Final stage
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates tzdata && rm -rf /var/lib/apt/lists/*

WORKDIR /dashboard

COPY --from=builder /app/dashboard ./app
COPY script/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME ["/dashboard/data"]
EXPOSE 8008
ARG TZ=Asia/Shanghai
ENV TZ=$TZ

ENTRYPOINT ["/entrypoint.sh"]
