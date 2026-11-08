package researchchat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"canonkeeper/backend/internal/auth"
	"canonkeeper/backend/internal/domain/agent"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
)

const steerTurnOperation = "research_chat.turn.steer.v1"

// steerBudget bounds one correction: the retries and the native call run past a dropped connection,
// so the outcome is known and its key does not hang (specs/2026-10-30-chat-steering.md R1.5).
const steerBudget = 15 * time.Second

// steerRetryDelays are the waits of a turn that takes no correction for a moment (R2.2).
var steerRetryDelays = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

type SteerTurnInput struct {
	Text string
	// Images are the numbers of the gallery images attached to the correction; FirstPhoto is the
	// anchor of the first of them, named by the web as the author sees it (R4.2).
	Images            []int
	FirstPhoto        int
	IdempotencyKey    string
	RequestMethod     string
	RequestPath       string
	SessionWriteProof auth.SessionWriteProof
}

type BeginSteerCommand struct {
	ActorUserID        string
	ChatID             string
	TurnID             string
	Session            researchdomain.Session
	Images             []int
	SessionWriteProof  auth.SessionWriteProof
	ScopeHash          string
	OperationID        string
	IdempotencyKeyHash string
	RequestHash        string
	CreatedAt          time.Time
}

// SteerTarget is what a correction goes into. Replayed: this key was accepted before and nothing is
// sent again. An empty ProviderTurnID is a turn that has not started.
type SteerTarget struct {
	// Flow is the app-server of the chat session's kind.
	Flow           agent.Flow
	RecordID       string
	ProjectID      string
	ChatID         string
	TurnID         string
	JobID          string
	RootThreadID   string
	ProviderTurnID string
	ImageTitles    map[int]string
	Replayed       bool
}

type SteerRepository interface {
	GetSession(context.Context, researchdomain.Actor, string) (researchdomain.Session, error)
	// BeginSteer checks the right and the turn and takes the idempotency key.
	BeginSteer(context.Context, BeginSteerCommand) (SteerTarget, error)
	// ReloadSteerTarget reads the turn again; ErrTurnEnded once it stopped working.
	ReloadSteerTarget(context.Context, SteerTarget) (SteerTarget, error)
	// FinishSteer keeps the key of an accepted correction and lets go of any other.
	FinishSteer(context.Context, SteerTarget, bool) error
}

// TurnSteerRequest is one correction for the native turn working now.
type TurnSteerRequest struct {
	Flow            agent.Flow
	RootThreadID    string
	TurnID          string
	ClientMessageID string
	Input           string
}

// TurnSteerer returns agent.ErrTurnNotActive or agent.ErrTurnNotSteerable for a refusal of the turn.
type TurnSteerer func(context.Context, TurnSteerRequest) error

type SteerTurn struct {
	repository SteerRepository
	steer      TurnSteerer
	clock      Clock
	delays     []time.Duration
}

func NewSteerTurn(repository SteerRepository, steer TurnSteerer, clock Clock) *SteerTurn {
	if repository == nil || steer == nil || clock == nil {
		panic("research chat: steer-turn dependencies are required")
	}
	return &SteerTurn{repository: repository, steer: steer, clock: clock, delays: steerRetryDelays}
}

// Execute sends the author's correction into the turn and returns its id.
func (useCase *SteerTurn) Execute(ctx context.Context, actor researchdomain.Actor, chatID, turnID string, input SteerTurnInput) (string, error) {
	actor.UserID = strings.TrimSpace(actor.UserID)
	chatID = strings.TrimSpace(chatID)
	turnID = strings.TrimSpace(turnID)
	input.Text = strings.TrimSpace(input.Text)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.RequestMethod = strings.ToUpper(strings.TrimSpace(input.RequestMethod))
	input.RequestPath = strings.TrimSpace(input.RequestPath)
	if ctx == nil || actor.UserID == "" || chatID == "" || turnID == "" || input.RequestMethod == "" || input.RequestPath == "" ||
		!validIdempotencyKey(input.IdempotencyKey) || !input.SessionWriteProof.Valid() || !validQuestion(input.Text) ||
		!researchdomain.ValidAttachedImages(input.Images) || !validFirstPhoto(input.Images, input.FirstPhoto) {
		return "", researchdomain.ErrValidation
	}
	session, err := useCase.repository.GetSession(ctx, actor, chatID)
	if err != nil {
		return "", err
	}
	if session.Scope != researchdomain.ScopeProject || session.BookID != "" {
		return "", researchdomain.ErrNotFound
	}
	if session.AgentGeneration != researchdomain.CurrentAgentGeneration {
		return "", researchdomain.ErrOutdatedAgent
	}
	request, err := json.Marshal(struct {
		ChatID     string `json:"chatId"`
		TurnID     string `json:"turnId"`
		Text       string `json:"text"`
		Images     []int  `json:"images,omitempty"`
		FirstPhoto int    `json:"firstPhoto,omitempty"`
	}{chatID, turnID, input.Text, input.Images, input.FirstPhoto})
	if err != nil {
		return "", err
	}
	keyHash := hashText(input.IdempotencyKey)
	steerID := "steer-" + hashText(turnID + "\x00" + keyHash)[:32]
	target, err := useCase.repository.BeginSteer(ctx, BeginSteerCommand{
		ActorUserID: actor.UserID, ChatID: chatID, TurnID: turnID, Session: session,
		Images: append([]int(nil), input.Images...), SessionWriteProof: input.SessionWriteProof,
		ScopeHash:   requestScopeHash(actor.UserID, input.RequestMethod, input.RequestPath),
		OperationID: steerTurnOperation, IdempotencyKeyHash: keyHash,
		RequestHash: hashText(string(request)), CreatedAt: useCase.clock.Now(),
	})
	if err != nil {
		return "", err
	}
	if target.Replayed {
		return steerID, nil
	}
	encoded, err := encodeAssignment(assignmentInput{
		Text: input.Text, SourceView: session.SourceView, Scope: session.Scope, BookID: session.BookID,
		Images: input.Images, ImageTitles: target.ImageTitles, FirstPhoto: input.FirstPhoto, Correction: true,
	})
	if err != nil {
		return "", errors.Join(err, useCase.repository.FinishSteer(context.WithoutCancel(ctx), target, false))
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), steerBudget)
	defer cancel()
	sent := useCase.send(work, target, steerID, encoded)
	if err := useCase.repository.FinishSteer(work, target, sent == nil); err != nil {
		return "", errors.Join(sent, err)
	}
	if sent != nil {
		return "", sent
	}
	return steerID, nil
}

// send retries a turn that works but takes no correction for a moment and stops as soon as the Job
// has ended (R2.1, R2.2).
func (useCase *SteerTurn) send(ctx context.Context, target SteerTarget, steerID, input string) error {
	for attempt := 0; ; attempt++ {
		if target.ProviderTurnID != "" {
			err := useCase.steer(ctx, TurnSteerRequest{
				Flow: target.Flow, RootThreadID: target.RootThreadID, TurnID: target.ProviderTurnID, ClientMessageID: steerID, Input: input,
			})
			if err == nil {
				return nil
			}
			if !errors.Is(err, agent.ErrTurnNotActive) && !errors.Is(err, agent.ErrTurnNotSteerable) {
				return err
			}
		}
		if attempt == len(useCase.delays) {
			return researchdomain.ErrSteerRetry
		}
		select {
		case <-time.After(useCase.delays[attempt]):
		case <-ctx.Done():
			return ctx.Err()
		}
		reloaded, err := useCase.repository.ReloadSteerTarget(ctx, target)
		if err != nil {
			return err
		}
		target = reloaded
	}
}

// validFirstPhoto: an anchor is named only for the images a correction carries.
func validFirstPhoto(images []int, first int) bool {
	if len(images) == 0 {
		return first == 0
	}
	return first >= 1 && first <= researchdomain.MaxFirstPhoto
}
