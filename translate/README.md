# translate

Перевод протоколов для «Своего ИИ» настольного приложения CanonKeeper. Codex и ядро говорят с локальным шлюзом на
языке Responses, а провайдер автора — на своём: модуль либо пробрасывает запрос Responses как есть, либо переводит его
в Chat Completions, а поток Chat — обратно в события Responses.

Модуль взят из codex-lb и живёт отдельно от AGPL-кода репозитория: свой `go.mod`, лицензия MIT (`LICENSE`,
`LICENSE.codex-relay`), откуда что взято и что изменено — `NOTICE`. Внешних зависимостей нет.

## Как звать

```go
translator := translate.New(httpClient) // один на шлюз, безопасен для одновременных вызовов
defer translator.Close()

target := translate.Target{
	BaseURL:    "http://127.0.0.1:11434/v1", // адрес API провайдера; локальные и частные адреса разрешены
	Credential: key,                         // ключ провайдера; в текст сбоя он не попадает
	Model:      "qwen3:32b",                 // модель провайдера вместо модели запроса (пусто — как в запросе)
	Capabilities: translate.Capabilities{
		Protocol:  translate.ProtocolChatCompletions,
		Tools:     true,
		Reasoning: true,
	},
}
result, err := translator.OpenStream(ctx, target, translate.Request{Body: body}, func(event translate.Event) error {
	return writeSSE(event.Type, event.Data) // события Responses по порядку, последнее — итог
})
```

Запрос без потока (`"stream": false`) — `translator.Execute(ctx, target, request)`: итог с ответом Responses целиком.

## Открытые имена

Всё, что видит шлюз, — пакет `translate`; устройство перевода лежит в `internal/` и в лицо не входит.

- `New(*http.Client) *Translator` и у `Translator` — `OpenStream`, `Execute`, `Close`. Перенаправление провайдера
  модуль не исполняет: ключ и тело на чужой адрес не уходят.
- `Target` — `BaseURL`, `Credential`, `Model`, `Headers` (заголовки провайдеру рядом со своими модуля; заголовки
  клиента сами не проходят, а `Authorization`, `Cookie`, `Host`, `Content-Type`, `Content-Length`, `Accept`,
  `Transfer-Encoding`, `Connection` цель назвать не может) и `Capabilities`.
- `Capabilities` — профиль провайдера:
  - `Protocol` — `ProtocolResponses` (проброс) или `ProtocolChatCompletions` (перевод);
  - `Tools`, `ParallelToolCalls`, `ImageInput` — что провайдер принимает; запрос сверх этого — отказ
    `unsupported_capability`;
  - `Reasoning` — модель рассуждает; без него рассуждения запроса не уходят (и `reasoning.encrypted_content` из
    `include`), а не отклоняют запрос;
  - `MaxReasoningEffort` — самое высокое усилие провайдера, выше — понижается до него; пусто — `high`: большинство
    провайдеров Chat Completions выше не принимают;
  - `ExtraBody`, `DropBodyFields` — поля, которые модуль добавляет к телу провайдеру и убирает из него (у проброса
    Responses так убираются `client_metadata`, `prompt_cache_key`);
  - `EnableGLMThinking` — `thinking: {type: enabled}` для GLM;
  - `ThinkTags` — разметка `<think>`, `<thinking>`, `◁think▷` в тексте ответа уходит в рассуждения;
  - `AllowNoSSEDone`, `AllowMissingSSEContentType` — поток без `[DONE]` (при `finish_reason`) и без
    `Content-Type: text/event-stream` принимается;
  - `MaxRequestBodyBytes` — потолок тела запроса. Задаёт вызывающий по окну модели; пусто — 64 МиБ. Codex шлёт всю
    нить в каждом запросе: окно в 1 млн токенов — это мегабайты текста, а картинки и выводы инструментов умножают их;
    64 МиБ дают запас на порядок и держат память одного запроса в пределах (тело и его перевод живут в памяти разом).
    Тело больше потолка — отказ `request_too_large` до обращения к провайдеру.
- `Request` — тело запроса Responses, как его прислал клиент.
- `Event` — событие Responses: `Type` и `Data` (JSON события).
- `Result` — итог: `ResponseID`, `Response` (объект ответа Responses; у отказа провайдера его нет — текст отказа в
  `Error.Body`), `Usage` и `UsageKnown` (провайдер мог не
  прислать usage — тогда токены неизвестны, а не нули; `UsageReported` — usage пришёл, но непригоден), `ServiceTier`,
  `Failed` (итог не `completed`: отказ или обрезанный ответ), `IncompleteReason` (почему ответ обрезан, например
  `max_output_tokens`), `ErrorCode`, `ErrorMessage`.
- `Usage` — `InputTokens`, `OutputTokens`, `TotalTokens` (всегда сумма первых двух), `CachedTokens`,
  `ReasoningTokens`, `ReasoningTokensKnown`.
- `Error` — ошибка модуля: `Code`, `Status` (HTTP-код отказа провайдера), `Message`, `Body` (тело отказа провайдера,
  до 4 КиБ), `RetryAfter` (его `Retry-After`: секунды или дата), `RejectedBeforeExecution` (провайдер доказал, что
  запрос не исполнялся). Коды — константы `ErrorCode…`:
  - отказ провайдера — его код (`rate_limit_exceeded`, `insufficient_quota`, `http_<статус>` и т. д.);
  - `unsupported_capability` — запрос просит то, чего у провайдера нет: встроенные инструменты Responses
    (`web_search`, `local_shell`, `image_generation`, `code_interpreter` и другие) и их элементы входа, картинки,
    `previous_response_id` у Chat Completions; провайдер при этом не вызывается;
  - `invalid_request`, `request_too_large`, `invalid_configuration` — неверный запрос, тело больше потолка, неверная
    цель;
  - `stream_incomplete`, `connection_error`, `invalid_stream`, `invalid_tool_call` — обрыв потока, сбой соединения,
    неверный поток или вызов инструмента провайдера;
  - отмена — `errors.Is(err, context.Canceled)` или `context.DeadlineExceeded`.
- Текст сбоя — `Message` и `Body` ошибки, `ErrorMessage` итога, сообщение ошибки в его `Response` и в событии
  `response.failed` — выходит из модуля без ключа провайдера (на его месте `[provider key]`, даже если провайдер
  повторил ключ в отказе) и не длиннее 4 КиБ; ключ заменяется до обрезки, поэтому и часть ключа на границе не
  остаётся. Остальное модуль не меняет: ответ модели и события приходят как есть, и ключ-заглушка локального
  сервера (`x`, `ollama`) их не портит; в тексте сбоя такая заглушка заменяется везде, где стоит.

## Состав

- `translate.go` — лицо модуля;
- `internal/provider` — перевод и проброс;
- `internal/think` — разметка рассуждений из текста ответа в рассуждения;
- `internal/wirejson` — сборка JSON-объекта без перекодирования значений.

Проверки: `go vet ./... && go test -race -count=1 ./...` в этом каталоге; без сети, провайдеров заменяет `httptest`.
