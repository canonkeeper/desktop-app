package jobs

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

const (
	defaultPageLimit = 50
	maxPageLimit     = 100
	jobCursorVersion = 1
)

func pageLimit(page Page) (int, error) {
	if page.Limit == 0 {
		return defaultPageLimit, nil
	}
	if page.Limit < 1 || page.Limit > maxPageLimit {
		return 0, ErrValidation
	}
	return page.Limit, nil
}

type jobListCursorPayload struct {
	Version     int    `json:"v"`
	ActorUserID string `json:"actorUserId"`
	ProjectID   string `json:"projectId"`
	Kind        string `json:"kind"`
	Active      bool   `json:"active,omitempty"`
	CreatedAt   string `json:"createdAt"`
	ID          string `json:"id"`
}

type decodedJobListCursor struct {
	ActorUserID string
	ProjectID   string
	Kind        string
	Active      bool
	CreatedAt   time.Time
	ID          string
}

// encodeJobListCursor binds the cursor to the actor, the project and both filters: a cursor of
// another list is rejected.
func encodeJobListCursor(actorUserID, projectID string, kind Kind, active bool, job Job) (string, error) {
	payload := jobListCursorPayload{
		Version:     jobCursorVersion,
		ActorUserID: actorUserID,
		ProjectID:   projectID,
		Kind:        string(kind),
		Active:      active,
		CreatedAt:   job.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:          job.ID,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeJobListCursor(raw string) (*decodedJobListCursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, ErrValidation
	}
	var payload jobListCursorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, ErrValidation
	}
	if payload.Version != jobCursorVersion || payload.ActorUserID == "" || payload.ProjectID == "" || payload.CreatedAt == "" || payload.ID == "" {
		return nil, ErrValidation
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil {
		return nil, ErrValidation
	}
	return &decodedJobListCursor{
		ActorUserID: payload.ActorUserID,
		ProjectID:   payload.ProjectID,
		Kind:        payload.Kind,
		Active:      payload.Active,
		CreatedAt:   createdAt,
		ID:          payload.ID,
	}, nil
}
