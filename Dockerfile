# ---- builder ----
FROM golang:1.24-alpine AS builder

WORKDIR /src

# Cache module downloads separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /code-mcp ./cmd/code-mcp

# ---- runtime ----
FROM debian:bookworm-slim

# git              – needed for clone/worktree operations via the management API
# ca-certificates  – needed for HTTPS clones
RUN apt-get update -qq \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:$PATH"

COPY --from=builder /code-mcp /usr/local/bin/code-mcp
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Default repos root; mount a volume here for persistence.
RUN mkdir -p /repos
VOLUME ["/repos"]

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
