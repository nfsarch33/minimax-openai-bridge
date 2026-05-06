# minimax-openai-bridge

Go HTTP adapter that exposes an OpenAI-compatible embeddings endpoint and
translates requests to MiniMax `embo-01`.

It exists so Mem0 OSS can use MiniMax embeddings without teaching every caller
MiniMax's native `{texts, type}` schema.

## API

```text
POST /v1/embeddings
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
| `MINIMAX_API_KEY` | required | MiniMax API key. |
| `MINIMAX_GROUP_ID` | empty | Optional MiniMax group ID query parameter. |
| `MINIMAX_EMBEDDING_MODEL` | `embo-01` | Embedding model. |
| `MINIMAX_EMBEDDING_TYPE` | `db` | Default MiniMax embedding type. |

Keep `MINIMAX_API_KEY` in 1Password or target-host env only. Never pass it on
argv.

## Development

```bash
go test ./...
go run ./cmd/minimax-openai-bridge
```
