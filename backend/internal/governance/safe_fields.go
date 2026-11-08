package governance

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"strings"
	"time"
)

type Count uint64

// Measure is a measured operational number — a rate, a delay in seconds —, finite and not below zero.
type Measure float64

func NewMeasure(value float64) Measure {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return Measure(value)
}

type DurationMillis uint64

const maxOperationalDuration = 24 * time.Hour

func NewDurationMillis(value time.Duration) (DurationMillis, error) {
	if value < 0 || value > maxOperationalDuration {
		return 0, errors.New("governance: invalid operational duration")
	}
	return DurationMillis(value / time.Millisecond), nil
}

type Digest [32]byte

func (digest Digest) Validate() error {
	return nil
}

func (digest Digest) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(digest[:]))
}

func (digest *Digest) UnmarshalJSON(raw []byte) error {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return errors.New("governance: invalid digest")
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != len(digest) {
		return errors.New("governance: invalid digest")
	}
	copy(digest[:], decoded)
	return nil
}

type RequestID string

func NewRequestID(value string) (RequestID, error) {
	requestID := RequestID(value)
	if err := requestID.Validate(); err != nil {
		return "", err
	}
	return requestID, nil
}

func (id RequestID) Validate() error {
	if _, err := parseCanonicalUUID(string(id)); err != nil {
		return errors.New("governance: invalid request ID")
	}
	return nil
}

type HTTPMethod string

const (
	HTTPMethodGet     HTTPMethod = "GET"
	HTTPMethodPost    HTTPMethod = "POST"
	HTTPMethodPut     HTTPMethod = "PUT"
	HTTPMethodPatch   HTTPMethod = "PATCH"
	HTTPMethodDelete  HTTPMethod = "DELETE"
	HTTPMethodHead    HTTPMethod = "HEAD"
	HTTPMethodOptions HTTPMethod = "OPTIONS"
)

func NewHTTPMethod(value string) (HTTPMethod, error) {
	method := HTTPMethod(value)
	switch method {
	case HTTPMethodGet, HTTPMethodPost, HTTPMethodPut, HTTPMethodPatch,
		HTTPMethodDelete, HTTPMethodHead, HTTPMethodOptions:
		return method, nil
	default:
		return "", errors.New("governance: invalid HTTP method")
	}
}

type HTTPStatus uint16

func NewHTTPStatus(value int) (HTTPStatus, error) {
	if value < 100 || value > 599 {
		return 0, errors.New("governance: invalid HTTP status")
	}
	return HTTPStatus(value), nil
}

type RequestErrorClass string

const (
	RequestErrorContext  RequestErrorClass = "context"
	RequestErrorDatabase RequestErrorClass = "database"
	RequestErrorInternal RequestErrorClass = "internal"
)

type RequestErrorCause string

const (
	RequestErrorCanceled         RequestErrorCause = "canceled"
	RequestErrorDeadlineExceeded RequestErrorCause = "deadline_exceeded"
	RequestErrorUnclassified     RequestErrorCause = "unclassified"
	RequestErrorPanic            RequestErrorCause = "panic"
)

func NewRequestErrorSQLState(value string) (RequestErrorCause, error) {
	if len(value) != 5 {
		return "", errors.New("governance: invalid SQLSTATE")
	}
	for index := range value {
		if (value[index] < '0' || value[index] > '9') && (value[index] < 'A' || value[index] > 'Z') {
			return "", errors.New("governance: invalid SQLSTATE")
		}
	}
	return RequestErrorCause(value), nil
}

// maxRequestErrorCodeBytes ограничивает код стадии: `timeline.history_list_failed` и подобные.
const maxRequestErrorCodeBytes = 64

// NewRequestErrorCode принимает статический код стадии сбоя (`SafeErrorCode` ошибки): не меньше
// двух частей через точку, каждая — строчные латинские буквы, цифры и `_`, первая — буква.
// Свободный текст, идентификаторы с дефисами и прописные буквы отклоняются.
func NewRequestErrorCode(value string) (RequestErrorCause, error) {
	invalid := errors.New("governance: invalid request error code")
	if len(value) > maxRequestErrorCodeBytes || !strings.Contains(value, ".") {
		return "", invalid
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || part[0] < 'a' || part[0] > 'z' {
			return "", invalid
		}
		for index := range part {
			if (part[index] < 'a' || part[index] > 'z') && (part[index] < '0' || part[index] > '9') && part[index] != '_' {
				return "", invalid
			}
		}
	}
	return RequestErrorCause(value), nil
}

// PanicText — значение или стек паники обработчика для служебного журнала. Его создаёт только
// observability.RecoveredPanic: текст обрезан и не содержит запроса, тела или заголовков.
type PanicText string

type RouteOperationID string

func NewRouteOperationID(value string) (RouteOperationID, error) {
	operation := RouteOperationID(value)
	if err := operation.Validate(); err != nil {
		return "", err
	}
	return operation, nil
}

func (operation RouteOperationID) Validate() error {
	value := string(operation)
	if len(value) == 0 || len(value) > 80 || !asciiLetter(value[0]) {
		return errors.New("governance: invalid route operation ID")
	}
	for index := 1; index < len(value); index++ {
		if !asciiLetter(value[index]) && (value[index] < '0' || value[index] > '9') {
			return errors.New("governance: invalid route operation ID")
		}
	}
	return nil
}

type RoutePathTemplate string

func NewRoutePathTemplate(value string) (RoutePathTemplate, error) {
	template := RoutePathTemplate(value)
	if err := template.Validate(); err != nil {
		return "", err
	}
	return template, nil
}

func (template RoutePathTemplate) Validate() error {
	value := string(template)
	if len(value) == 0 || len(value) > 256 || value[0] != '/' || strings.Contains(value, "//") {
		return errors.New("governance: invalid route path template")
	}
	for _, segment := range strings.Split(value[1:], "/") {
		if segment == "" {
			continue
		}
		if segment[0] == '{' || segment[len(segment)-1] == '}' {
			if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' ||
				!validRouteToken(segment[1:len(segment)-1]) {
				return errors.New("governance: invalid route path template")
			}
			continue
		}
		for index := 0; index < len(segment); index++ {
			char := segment[index]
			if !asciiLetter(char) && (char < '0' || char > '9') && char != '-' && char != '_' {
				return errors.New("governance: invalid route path template")
			}
		}
	}
	return nil
}

func validRouteToken(value string) bool {
	if len(value) == 0 || len(value) > 64 || !asciiLetter(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !asciiLetter(value[index]) && (value[index] < '0' || value[index] > '9') {
			return false
		}
	}
	return true
}

func asciiLetter(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

type ResourceID string

const ZeroResourceID ResourceID = "00000000-0000-0000-0000-000000000000"

func (id ResourceID) Validate() error {
	if _, err := parseCanonicalUUID(string(id)); err != nil {
		return errors.New("governance: invalid resource ID")
	}
	return nil
}

// ClientAddress names the client of an anonymous sign-in, registration or password reset in the
// request log: an address, or the IPv6 network the rate limiter counts it by, in canonical form
// (SEC-17).
type ClientAddress string

func NewClientAddress(value string) (ClientAddress, error) {
	if address, err := netip.ParseAddr(value); err == nil {
		return ClientAddress(address.String()), nil
	}
	if network, err := netip.ParsePrefix(value); err == nil {
		return ClientAddress(network.String()), nil
	}
	return "", errors.New("governance: invalid client address")
}

// VoiceCallID names a voice call in the request log by the gateway's id of it (rtc_…), so that the quality of its sound
// can be told apart from other calls (specs/2026-11-03-voice-assistant.md V0.11).
type VoiceCallID string

func NewVoiceCallID(value string) (VoiceCallID, error) {
	rest, ok := strings.CutPrefix(value, "rtc_")
	if !ok || rest == "" || len(rest) > 128 || strings.ContainsFunc(rest, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) {
		return "", errors.New("governance: invalid voice call id")
	}
	return VoiceCallID(value), nil
}

type ReasonCode string

const (
	ReasonPurged            ReasonCode = "purged"
	ReasonNoData            ReasonCode = "no_data"
	ReasonTargetUnavailable ReasonCode = "target_unavailable"
	ReasonStaleSource       ReasonCode = "stale_source"
	ReasonExpiredLease      ReasonCode = "expired_lease"
	ReasonValidationFailed  ReasonCode = "validation_failed"
)
