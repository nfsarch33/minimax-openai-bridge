FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /out/minimax-openai-bridge ./cmd/minimax-openai-bridge

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/minimax-openai-bridge /minimax-openai-bridge
USER 65534
EXPOSE 8500
ENTRYPOINT ["/minimax-openai-bridge"]
