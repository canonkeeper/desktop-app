// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"context"
	"errors"
	"io"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
)

// writeTranscript writes a chat whose transcript the reader takes; another is named in chatsNotRead.
func (prepared *Prepared) writeTranscript(entry domain.ChatEntry, turns []domain.ChatTurn) {
	entry.File, entry.Turns = domain.ChatFile(len(prepared.entries)+1), len(turns)
	if err := prepared.writer.Transcript(entry.File, domain.ChatTranscript{Turns: turns}); err != nil {
		prepared.notRead = append(prepared.notRead, entry.Title)
		return
	}
	prepared.entries = append(prepared.entries, entry)
}

// WriteTo sends the file: the part of the base, then each chat of the owner as its thread is read, then the manifest. An
// error after the first byte leaves the file without its manifest; the caller breaks the connection (SF2.3).
func (prepared *Prepared) WriteTo(ctx context.Context, out io.Writer) error {
	size, err := prepared.temp.Size()
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.NewSectionReader(prepared.temp, 0, size)); err != nil {
		return err
	}
	prepared.relay.out = out
	for _, chat := range prepared.chats {
		if err := ctx.Err(); err != nil {
			return err
		}
		prepared.writeChat(ctx, chat)
	}
	chats, err := prepared.writer.Chats()
	if err != nil {
		return err
	}
	for _, entry := range prepared.entries {
		if err := chats.Add(entry); err != nil {
			return err
		}
	}
	if err := chats.Close(); err != nil {
		return err
	}
	return prepared.writer.Close(domain.Manifest{
		CreatedBy: prepared.useCase.CreatedBy, CreatedAt: prepared.useCase.Now().UTC(),
		Saga: domain.ManifestSaga{Name: prepared.saga}, ChatsNotRead: prepared.notRead, LeftOut: prepared.leftOut,
	})
}

// writeChat reads the thread of a chat with turns of the owner and writes his turns (SF1.8): the question, the answer of a
// completed turn, the outcome, the title of the attached chapter; a thread that cannot be read is named, not lost.
func (prepared *Prepared) writeChat(ctx context.Context, chat ChatSource) {
	mine := 0
	for _, turn := range chat.Turns {
		if turn.Mine {
			mine++
		}
	}
	if mine == 0 {
		return
	}
	title := chatTitle(chat)
	turns, err := prepared.readTurns(ctx, chat)
	if err != nil || len(turns) == 0 {
		prepared.notRead = append(prepared.notRead, title)
		return
	}
	prepared.writeTranscript(domain.ChatEntry{
		Title: title, CreatedAt: chat.CreatedAt, LastTurnAt: maxTime(chat.CreatedAt, turns[len(turns)-1].AskedAt),
		OthersTurns: len(chat.Turns) - mine,
	}, turns)
}

func (prepared *Prepared) readTurns(ctx context.Context, chat ChatSource) ([]domain.ChatTurn, error) {
	if chat.ThreadID == "" {
		return nil, errors.New("saga archive: the chat has no thread")
	}
	flow, _ := agentdomain.SessionFlow(agentdomain.SessionResearch)
	items, err := prepared.useCase.Threads.ThreadItems(ctx, flow, chat.ThreadID)
	if err != nil {
		return nil, err
	}
	transcript, err := domain.Transcript(chat.ID, items)
	if err != nil {
		return nil, err
	}
	stored := make(map[string]TurnSource, len(chat.Turns))
	for _, turn := range chat.Turns {
		stored[turn.ProviderTurnID] = turn
	}
	turns := make([]domain.ChatTurn, 0, len(transcript))
	for _, turn := range transcript {
		source, known := stored[turn.ProviderTurnID]
		if !known || !source.Mine {
			continue
		}
		outcome := turn.Outcome
		if outcome == domain.TurnInProgress && source.Settled != "" {
			outcome = source.Settled
		}
		turns = append(turns, domain.ChatTurn{
			AskedAt: source.AskedAt.UTC(), Question: turn.Question, Answer: turn.Answer, Outcome: outcome, Attached: source.Attached,
		})
	}
	return turns, nil
}

// chatTitle is the title of a chat the owner began; a chat another member began is named by its date, so none of his
// words go into the file (SF1.8).
func chatTitle(chat ChatSource) string {
	if len(chat.Turns) > 0 && chat.Turns[0].Mine && domain.ValidLine(chat.Title, domain.MaxChatTitleRunes) {
		return chat.Title
	}
	return "Чат от " + chat.CreatedAt.UTC().Format("02.01.2006")
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b.UTC()
	}
	return a.UTC()
}
