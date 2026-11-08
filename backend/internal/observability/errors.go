package observability

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"unicode/utf8"

	"canonkeeper/backend/internal/governance"
)

const (
	maxPanicValueBytes = 500
	maxPanicStackBytes = 8 << 10
)

// PanicError — паника, пойманная на границе запроса, задачи или вызова инструмента: значение и
// стек горутины для журнала. Процесс после неё продолжает работать.
type PanicError struct {
	Value string
	Stack string
}

// RecoveredPanic оборачивает значение recover(); вызывается прямо в отложенной функции, чтобы
// стек содержал место паники.
func RecoveredPanic(recovered any) *PanicError {
	return &PanicError{
		Value: Truncate(fmt.Sprint(recovered), maxPanicValueBytes),
		Stack: Truncate(string(debug.Stack()), maxPanicStackBytes),
	}
}

func (err *PanicError) Error() string { return "panic: " + err.Value }

type sqlStateError interface {
	SQLState() string
}

// ClassifyError даёт безопасные для журнала класс и причину ошибки: отмена или срок контекста,
// SQLSTATE базы, паника, код стадии сбоя или внутренняя ошибка без класса. Текст ошибки в них
// не попадает.
func ClassifyError(err error) (governance.RequestErrorClass, governance.RequestErrorCause) {
	var panicked *PanicError
	switch {
	case errors.Is(err, context.Canceled):
		return governance.RequestErrorContext, governance.RequestErrorCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return governance.RequestErrorContext, governance.RequestErrorDeadlineExceeded
	case errors.As(err, &panicked):
		return governance.RequestErrorInternal, governance.RequestErrorPanic
	}
	var databaseError sqlStateError
	if errors.As(err, &databaseError) {
		if cause, causeErr := governance.NewRequestErrorSQLState(databaseError.SQLState()); causeErr == nil {
			return governance.RequestErrorDatabase, cause
		}
	}
	var staged safeCodedError
	if errors.As(err, &staged) {
		if cause, causeErr := governance.NewRequestErrorCode(staged.SafeErrorCode()); causeErr == nil {
			return governance.RequestErrorInternal, cause
		}
	}
	return governance.RequestErrorInternal, governance.RequestErrorUnclassified
}

// safeCodedError — ошибка со статическим кодом стадии сбоя (например, чтение ленты из
// app-server); код, а не текст ошибки, становится причиной в журнале.
type safeCodedError interface {
	SafeErrorCode() string
}

// Truncate обрезает текст для журнала до limit байт по границе символа UTF-8.
func Truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
