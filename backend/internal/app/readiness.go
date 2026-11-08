package app

import (
	"context"
	"io"
	"net/http"
	"time"
)

const readinessDatabaseTimeout = 2 * time.Second

type databasePing func(context.Context) error

// withReadiness добавляет локальную operational probe поверх существующего HTTP handler.
// Ответы намеренно содержат только закрытые status/reason без деталей подключения и ошибок.
// Без среды чата сайт работает, поэтому её отсутствие — degraded, а не down (R3.3).
func withReadiness(
	next http.Handler,
	pingProductDatabase databasePing,
	pingWorkerDatabase databasePing,
	chatRuntimeReady func() bool,
) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/readyz" {
			next.ServeHTTP(response, request)
			return
		}

		if pingDatabase(request.Context(), pingProductDatabase) != nil {
			writeReadiness(response, http.StatusServiceUnavailable, `{"status":"down","reason":"product_database_unavailable"}`)
			return
		}
		if pingWorkerDatabase != nil && pingDatabase(request.Context(), pingWorkerDatabase) != nil {
			writeReadiness(response, http.StatusOK, `{"status":"degraded","reason":"worker_database_unavailable"}`)
			return
		}
		if !chatRuntimeReady() {
			writeReadiness(response, http.StatusOK, `{"status":"degraded","reason":"chat_runtime_unavailable"}`)
			return
		}
		writeReadiness(response, http.StatusOK, `{"status":"ok"}`)
	})
}

func pingDatabase(ctx context.Context, ping databasePing) error {
	pingCtx, cancel := context.WithTimeout(ctx, readinessDatabaseTimeout)
	defer cancel()
	return ping(pingCtx)
}

func writeReadiness(response http.ResponseWriter, status int, body string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = io.WriteString(response, body)
}
