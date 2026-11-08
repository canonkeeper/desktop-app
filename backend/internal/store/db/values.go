package storedb

import (
	"context"
	"time"

	"canonkeeper/backend/internal/observability"

	"github.com/jackc/pgx/v5/pgtype"
)

// CreatorRequestID — id HTTP-запроса из контекста для jobs.creator_request_id: задание, созданное
// запросом, помнит его как код поддержки. У задания, поставленного воркером, его нет (NULL).
func CreatorRequestID(ctx context.Context) pgtype.UUID {
	requestID, ok := observability.RequestIDFromContext(ctx)
	if !ok {
		return pgtype.UUID{}
	}
	value, err := ParseUUID(string(requestID))
	if err != nil {
		return pgtype.UUID{}
	}
	return value
}

func ParseUUID(value string) (pgtype.UUID, error) {
	var uuid pgtype.UUID
	if err := uuid.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return uuid, nil
}

func Timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func UUIDString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return value.String()
}

func OptionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
