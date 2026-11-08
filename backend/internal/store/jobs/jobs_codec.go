package jobstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"

	"canonkeeper/backend/internal/jobs"
)

type storedProgress struct {
	Value *float64                `json:"value,omitempty"`
	Phase *jobs.ResearchChatPhase `json:"phase,omitempty"`
}

func MarshalImportProgress(progress jobs.ImportProgress, status jobs.Status) ([]byte, error) {
	if err := jobs.ValidateImportProgress(jobs.KindImport, status, &progress); err != nil {
		return nil, err
	}
	return json.Marshal(progress)
}

func ParseImportProgress(data []byte, status jobs.Status) (*jobs.ImportProgress, error) {
	var progress jobs.ImportProgress
	if err := decodeStoredJSON(data, &progress); err != nil {
		return nil, err
	}
	if err := jobs.ValidateImportProgress(jobs.KindImport, status, &progress); err != nil {
		return nil, err
	}
	return &progress, nil
}

func parseJobProgress(data []byte, kind jobs.Kind, status jobs.Status) (*float64, *jobs.ResearchChatPhase, error) {
	if kind == jobs.KindImport {
		return nil, nil, jobs.ErrConflict
	}
	var progress storedProgress
	if err := decodeStoredJSON(data, &progress); err != nil {
		return nil, nil, err
	}
	if progress.Value != nil && (math.IsNaN(*progress.Value) || math.IsInf(*progress.Value, 0)) {
		return nil, nil, jobs.ErrConflict
	}
	if kind != jobs.KindCanonTextIndex && kind != jobs.KindChapterCards && kind != jobs.KindSagaLayer && progress.Value != nil {
		return nil, nil, jobs.ErrConflict
	}
	phase, err := jobs.NormalizeResearchChatPhase(kind, status, progress.Phase)
	if err != nil {
		return nil, nil, err
	}
	if progress.Value == nil {
		return nil, phase, nil
	}
	value := clampStoredProgress(*progress.Value)
	return &value, phase, nil
}

func marshalJobResult(result jobs.Result) ([]byte, error) {
	if result == nil {
		return nil, nil
	}
	return jobs.EncodeResult(result.Kind(), result)
}

func parseJobResult(kind jobs.Kind, data []byte) (jobs.Result, error) {
	if emptyJSON(data) {
		return nil, nil
	}
	return jobs.DecodeResult(kind, data)
}

func marshalJobError(jobError *jobs.Error) ([]byte, error) {
	if jobError == nil {
		return nil, nil
	}
	return jobs.EncodeError(*jobError)
}

func parseJobError(data []byte) (*jobs.Error, error) {
	if emptyJSON(data) {
		return nil, nil
	}
	jobError, err := jobs.DecodeError(data)
	if err != nil {
		return nil, err
	}
	return &jobError, nil
}

func decodeStoredJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return jobs.ErrConflict
	}
	return nil
}

func clampStoredProgress(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		if value <= 100 {
			return value / 100
		}
		return 1
	}
	return value
}

func emptyJSON(data []byte) bool {
	return len(data) == 0
}
