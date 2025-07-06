FROM golang:1.24-bullseye AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y \
    git \
    libsqlite3-dev \
    gcc \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the webserver
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o webserver ./cmd/webserver

# Final stage
FROM debian:bullseye-slim

# Install runtime dependencies for SQLite
RUN apt-get update && apt-get install -y \
    ca-certificates \
    sqlite3 \
    && rm -rf /var/lib/apt/lists/*

# Create app directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/webserver .

# Copy templates directory
COPY --from=builder /app/templates ./templates

# Create directories for logs and database
RUN mkdir -p /app/log /app/data

# Expose port
EXPOSE 8180

# Set environment variable for port
ENV PORT=8180

# Run the webserver
CMD ["./webserver"] 