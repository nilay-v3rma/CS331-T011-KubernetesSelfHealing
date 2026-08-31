# --- Build stage ---
FROM golang:1.22-alpine AS builder

WORKDIR /workspace

# Copy module files first for layer caching
COPY go.mod ./
# go.sum may not exist yet for the stub
COPY go.sum* ./
RUN go mod download 2>/dev/null || true

# Copy source
COPY cmd/manager/ cmd/manager/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o manager ./cmd/manager/

# --- Runtime stage ---
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
