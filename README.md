# minimax-openai-bridge

Go HTTP adapter that exposes an OpenAI-compatible API and translates requests
to MiniMax endpoints. Designed for Mem0 OSS integration.

**Key features:**

- **Embedding translation**: converts OpenAI `POST /v1/embeddings` to MiniMax `embo-01` format
- **Chat completions proxy**: forwards `POST /v1/chat/completions` to MiniMax
- **`<think>` tag stripping**: removes `<think>...</think>` reasoning blocks from MiniMax M2.7-highspeed responses (both streaming and non-streaming) so downstream consumers like Mem0 OSS receive clean JSON
- **Key rotation**: multiple API key slots with automatic failover on 429/quota errors

## API

```text
POST /v1/embeddings
POST /v1/chat/completions
GET  /healthz
```

OpenAI input:

```json
{
  "model": "embo-01",
  "input": ["text to embed"],
  "user": "db"
}
```

The optional `user` field is used as the MiniMax embedding type:

- `db` for memories/documents that will be stored.
- `query` for search queries.

## Configuration

| Env var | Default | Purpose |
| --- | --- | --- |
| `BRIDGE_ADDR` | `127.0.0.1:8500` | Listen address. |
| `MINIMAX_BASE_URL` | `https://api.minimax.chat/v1` | MiniMax API base URL. |
| `MINIMAX_API_KEY_1` | optional | Primary MiniMax key slot. |
| `MINIMAX_API_KEY_2` | optional | Secondary MiniMax key slot used when slot 1 is quota/rate-limited. |
| `MINIMAX_API_KEYS` | optional | Comma-separated extra key slots, evaluated before numbered slots. |
| `MINIMAX_API_KEY` | optional | Backwards-compatible single key fallback. |
| `MINIMAX_GROUP_ID` | empty | Optional MiniMax group ID query parameter. |
| `MINIMAX_EMBEDDING_MODEL` | `embo-01` | Embedding model. |
| `MINIMAX_EMBEDDING_TYPE` | `db` | Default MiniMax embedding type. |

Keep all MiniMax keys in a secrets manager or host-local env files. Never pass
them on argv.

## Deployment on your-host (Mem0 OSS stack)

### 1. Copy the linux binary

```bash
scp bin/minimax-openai-bridge-linux your-host:~/minimax-openai-bridge
ssh your-host 'chmod +x ~/minimax-openai-bridge'
```

### 2. Create a systemd unit

```bash
# /etc/systemd/system/minimax-openai-bridge.service
[Unit]
Description=MiniMax OpenAI Bridge
After=network.target

[Service]
Type=simple
User=<your-user>
ExecStart=/home/<your-user>/minimax-openai-bridge
Environment=BRIDGE_ADDR=127.0.0.1:8500
Environment=MINIMAX_BASE_URL=https://api.minimaxi.com/v1
EnvironmentFile=/home/<your-user>/.config/minimax-bridge/env
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Put API keys in `~/.config/minimax-bridge/env`:
```
MINIMAX_API_KEY_1=<your-primary-key>
MINIMAX_API_KEY_2=<your-secondary-key>
```

### 3. Update Mem0 OSS to point LLM at the bridge

In the Mem0 OSS Docker stack `.env` on your-host:
```
LLM_PROVIDER=openai
LLM_BASE_URL=http://127.0.0.1:8500/v1
LLM_API_KEY=unused-bridge-handles-auth
LLM_MODEL=MiniMax-M2.7-highspeed
EMBEDDING_PROVIDER=openai
EMBEDDING_BASE_URL=http://127.0.0.1:8500/v1
EMBEDDING_API_KEY=unused-bridge-handles-auth
EMBEDDING_MODEL=embo-01
```

### 4. Enable and start

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now minimax-openai-bridge
sudo systemctl restart mem0  # or docker compose restart
```

### 5. Verify

```bash
curl http://127.0.0.1:8500/healthz
# Expected: ok
```

## Development

```bash
go test -race ./...
go run ./cmd/minimax-openai-bridge
make build           # darwin binary
make docker-build    # Docker image
```

Cross-compile for linux:
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o bin/minimax-openai-bridge-linux ./cmd/minimax-openai-bridge
```

## License

MIT. See [LICENSE](LICENSE).
