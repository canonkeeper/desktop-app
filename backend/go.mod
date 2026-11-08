module canonkeeper/backend

go 1.26.10

require (
	github.com/coder/websocket v1.8.14
	github.com/go-chi/chi/v5 v5.2.3
	github.com/jackc/pgx/v5 v5.10.0
	github.com/openai/codex/sdk/go v0.165.4
	golang.org/x/crypto v0.53.0
	golang.org/x/image v0.45.0
	golang.org/x/text v0.41.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.22.0 // indirect
)

replace github.com/openai/codex/sdk/go v0.165.4 => github.com/Dirard/codex/sdk/go v0.165.4
