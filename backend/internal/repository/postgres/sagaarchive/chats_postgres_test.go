// SPDX-License-Identifier: AGPL-3.0-only

package sagaarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"canonkeeper/backend/internal/auth"
	agentdomain "canonkeeper/backend/internal/domain/agent"
	timeline "canonkeeper/backend/internal/domain/agenttimeline"
	materialsdomain "canonkeeper/backend/internal/domain/authormaterials"
	memorydomain "canonkeeper/backend/internal/domain/authormemory"
	researchdomain "canonkeeper/backend/internal/domain/researchchat"
	domain "canonkeeper/backend/internal/domain/sagaarchive"
	"canonkeeper/backend/internal/domain/sagamemory"
	"canonkeeper/backend/internal/governance"
	"canonkeeper/backend/internal/jobs"
	materialsrepo "canonkeeper/backend/internal/repository/postgres/authormaterials"
	memoryrepo "canonkeeper/backend/internal/repository/postgres/authormemory"
	researchrepo "canonkeeper/backend/internal/repository/postgres/researchchat"
	materialsservice "canonkeeper/backend/internal/services/authormaterials"
	memoryservice "canonkeeper/backend/internal/services/authormemory"
	researchservice "canonkeeper/backend/internal/services/researchchat"
	jobstore "canonkeeper/backend/internal/store/jobs"

	"github.com/jackc/pgx/v5"
)

// member is a member of a saga with his session.
type member struct {
	id    string
	proof auth.SessionWriteProof
}

// join makes a second member of the saga with his own session.
func (world *world) join(projectID, role string) member {
	world.t.Helper()
	var id string
	if err := world.db.Pool.QueryRow(world.ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		world.t.Fatal(err)
	}
	joined := member{id: id, proof: auth.SessionWriteProof{
		SessionHash: fmt.Sprintf("%x", sha256.Sum256([]byte(id+":session"))), CSRFSecretHash: fmt.Sprintf("%x", sha256.Sum256([]byte(id+":csrf"))),
	}}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id, email, display_name, password_hash, email_verified_at) VALUES ($1, $2, 'Соавтор', 'test-password-hash', now())`,
			[]any{id, "coauthor-" + id[:8] + "@example.test"}},
		{`INSERT INTO sessions (id, user_id, session_hash, csrf_secret_hash, expires_at, created_at)
		  VALUES (gen_random_uuid(), $1, $2, $3, clock_timestamp() + interval '1 hour', clock_timestamp())`,
			[]any{id, joined.proof.SessionHash, joined.proof.CSRFSecretHash}},
		{`INSERT INTO project_memberships (id, project_id, user_id, role, created_at) VALUES (gen_random_uuid(), $1, $2, $3, now())`,
			[]any{projectID, id, role}},
	} {
		if _, err := world.db.Pool.Exec(world.ctx, statement.query, statement.args...); err != nil {
			world.t.Fatal(err)
		}
	}
	return joined
}

// conversation asks the questions of a saga through the chat of the product: each turn is taken by the worker and bound
// to its thread, as the chat runs it.
type conversation struct {
	world   *world
	chats   *researchrepo.Repository
	project string
	next    int
	at      time.Time
}

func (talk *conversation) chat(who member, title string) researchdomain.Session {
	talk.world.t.Helper()
	talk.next++
	session, err := talk.chats.CreateSession(talk.world.ctx, researchservice.CreateSessionCommand{
		ID: fmt.Sprintf("e0000000-0000-4000-8000-%012d", talk.next), ProjectID: talk.project, ActorUserID: who.id,
		SourceView: string(governance.SourceViewPublished), Scope: researchdomain.ScopeProject, Title: title,
		SessionWriteProof: who.proof, ScopeHash: "chat-scope-" + who.id, OperationID: "research_chat.session.create.v1",
		IdempotencyKeyHash: fmt.Sprintf("chat-%d", talk.next), RequestHash: fmt.Sprintf("chat-request-%d", talk.next),
		StatusCode: 201, CreatedAt: talk.at,
	})
	if err != nil {
		talk.world.t.Fatalf("a chat of the saga: %v", err)
	}
	return session
}

// helper opens the session of a helper of a workshop: a chat session of its kind the list of chats does not show.
func (talk *conversation) helper(who member, kind string) researchdomain.Session {
	talk.world.t.Helper()
	var id string
	if err := talk.world.db.WithTx(talk.world.ctx, func(tx pgx.Tx) error {
		var err error
		id, err = talk.chats.CreateHelperSessionInTx(talk.world.ctx, tx, talk.project, who.id, "Помощник", kind, talk.at)
		return err
	}); err != nil {
		talk.world.t.Fatalf("the session of a helper %s: %v", kind, err)
	}
	return researchdomain.Session{ID: id, ProjectID: talk.project}
}

// ask asks a question, binds its turn to the thread of the chat under the id of the turn of the app-server and ends it as
// the chat does: with the answer, or failed when there is none.
func (talk *conversation) ask(who member, session researchdomain.Session, question, thread, turn, answer string, attached *researchdomain.Attachment) {
	talk.world.t.Helper()
	talk.next++
	talk.at = talk.at.Add(time.Minute)
	job, err := talk.chats.SubmitTurn(talk.world.ctx, researchservice.SubmitTurnCommand{
		ActorUserID: who.id, Session: session, Question: question, Attachment: attached, SessionWriteProof: who.proof,
		ScopeHash: "turn-scope-" + who.id, OperationID: "research_chat.turn.create.v1",
		IdempotencyKeyHash: fmt.Sprintf("turn-%d", talk.next), RequestHash: fmt.Sprintf("turn-request-%d", talk.next), CreatedAt: talk.at,
	})
	if err != nil {
		talk.world.t.Fatalf("the question %q: %v", question, err)
	}
	claim := talk.claim()
	subject, ok := job.Subject.(jobs.ResearchChatTurnSubject)
	if !ok || claim.JobID != job.ID {
		talk.world.t.Fatalf("the worker took %s, the question is %s", claim.JobID, job.ID)
	}
	run := researchdomain.TurnRun{ProjectID: talk.project, ChatID: session.ID, TurnID: subject.TurnID, JobID: job.ID, ActorUserID: who.id,
		ClaimIncarnation: claim.ClaimIncarnation}
	if err := talk.chats.BindRoot(talk.world.ctx, run, thread); err != nil {
		talk.world.t.Fatal(err)
	}
	if err := talk.chats.BindTurn(talk.world.ctx, run, thread, turn); err != nil {
		talk.world.t.Fatal(err)
	}
	if answer == "" {
		err = talk.chats.FinishFailure(talk.world.ctx, researchdomain.FinishFailure{ProjectID: talk.project, ChatID: session.ID, TurnID: subject.TurnID,
			JobID: job.ID, ClaimIncarnation: claim.ClaimIncarnation, Code: researchdomain.CodeTurnFailed})
	} else {
		err = talk.chats.FinishResponse(talk.world.ctx, researchdomain.FinishResponse{ProjectID: talk.project, ChatID: session.ID, TurnID: subject.TurnID,
			JobID: job.ID, ClaimIncarnation: claim.ClaimIncarnation, RootThreadID: thread, ProviderTurnID: turn, Response: answer})
	}
	if err != nil {
		talk.world.t.Fatalf("the end of the turn %q: %v", question, err)
	}
}

// claim takes the next question as the worker of the chat does.
func (talk *conversation) claim() jobs.WorkerClaim {
	return talk.world.claim(jobs.KindResearchChatTurn)
}

// threadOf is the root thread of a chat as the app-server keeps it: for each turn the question, the corrections of its
// author and the answer.
type threadOf map[string][]timeline.RawItem

func (threads threadOf) ThreadItems(_ context.Context, _ agentdomain.Flow, thread string) ([]timeline.RawItem, error) {
	return threads[thread], nil
}

func message(t *testing.T, turn string, status timeline.TurnStatus, raw map[string]any) timeline.RawItem {
	t.Helper()
	item, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	return timeline.RawItem{Role: timeline.RoleMain, TurnID: turn, TurnStatus: status, Item: item}
}

// turnItems are the items of one turn: the question, a correction when there is one, the answer of a completed turn.
func turnItems(t *testing.T, turn string, status timeline.TurnStatus, question, correction, answer string) []timeline.RawItem {
	t.Helper()
	asked := func(text string, corrected bool) map[string]any {
		assignment := map[string]any{"question": text, "projectRef": "project", "sourceView": "published", "scope": "project"}
		if corrected {
			assignment["correction"] = true
		}
		body, err := json.Marshal(assignment)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"type": "userMessage", "id": "user-" + turn + fmt.Sprint(corrected), "content": []map[string]string{{"type": "text", "text": string(body)}}}
	}
	items := []timeline.RawItem{message(t, turn, status, asked(question, false))}
	if correction != "" {
		items = append(items, message(t, turn, status, asked(correction, true)))
	}
	if answer != "" {
		items = append(items, message(t, turn, status, map[string]any{"type": "agentMessage", "id": "agent-" + turn, "text": answer, "phase": "final_answer"}))
	}
	return items
}

// specs/2026-11-13-desk-sagafile.md SF1.7, SF1.8: in a saga with two members the file carries the records of the saga of both
// and the turns of its owner only. His correction inside his turn, the turns of the other member, the title of a chat the
// other began and a chat without a turn of the owner stay out; the sessions of the helpers of a drawing and of a chapter are
// neither carried nor counted; a turn is the owner's by its Job, its outcome a failed Job says, its attached chapter goes by
// its title.
func TestTheFileCarriesTheTurnsOfItsOwnerOnly(t *testing.T) {
	world := newWorld(t, "ck_saga_file_members")
	opened := world.mustOpen(northernLimit(t, fixtureOptions{}), "members-key")
	world.index(opened.ProjectID, governance.SourceViewDraft)
	world.index(opened.ProjectID, governance.SourceViewPublished)
	owner := member{id: world.user, proof: world.proof}
	second := world.join(opened.ProjectID, "editor")
	talk := &conversation{world: world, chats: researchrepo.New(world.db), project: opened.ProjectID, at: time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)}
	completed, inProgress := timeline.TurnStatusCompleted, timeline.TurnStatusInProgress

	// The chat of the owner: his question with a chapter and a correction, a question of the second member, his question
	// whose Job failed.
	ours := talk.chat(owner, "Кто ждёт Мару")
	attached := &researchdomain.Attachment{Title: "Набросок главы", Format: "text", Paragraphs: []string{"Мара спустилась с перевала."}, Words: 4}
	talk.ask(owner, ours, "Кто ждёт Мару на заставе?", "thread-ours", "t1", "Тарен ждёт её третий день.", attached)
	talk.ask(second, ours, "Тайное имя второго", "thread-ours", "t2", "Ответ второму.", nil)
	talk.ask(owner, ours, "Сколько дней ждал Тарен?", "thread-ours", "t3", "", nil)
	// A chat the second member began, where the owner asked too, and one where only the second member asked.
	theirsAt := talk.at
	theirs := talk.chat(second, "Секрет второго")
	talk.ask(second, theirs, "Вопрос второго в его чате", "thread-theirs", "t4", "Ответ второму в его чате.", nil)
	talk.ask(owner, theirs, "Где стоит маяк?", "thread-theirs", "t5", "На краю льдов.", nil)
	alone := talk.chat(second, "Чат второго")
	talk.ask(second, alone, "Только второй спросил", "thread-alone", "t6", "Ответ.", nil)
	// The helpers of a drawing and of a chapter, with a question of the owner each.
	talk.ask(owner, talk.helper(owner, agentdomain.SessionDrawing), "Нарисуй маяк", "thread-drawing", "t7", "Рисую.", nil)
	talk.ask(owner, talk.helper(owner, agentdomain.SessionDraft), "Допиши главу о маяке", "thread-draft", "t8", "Пишу.", nil)

	world.export.Threads = threadOf{
		"thread-ours": slices.Concat(
			turnItems(t, "t1", completed, "Кто ждёт Мару на заставе?", "Только по первой книге", "Тарен ждёт её третий день."),
			turnItems(t, "t2", completed, "Тайное имя второго", "", "Ответ второму."),
			turnItems(t, "t3", inProgress, "Сколько дней ждал Тарен?", "", ""),
		),
		"thread-theirs": slices.Concat(
			turnItems(t, "t4", completed, "Вопрос второго в его чате", "", "Ответ второму в его чате."),
			turnItems(t, "t5", completed, "Где стоит маяк?", "", "На краю льдов."),
		),
		"thread-alone":   turnItems(t, "t6", completed, "Только второй спросил", "", "Ответ."),
		"thread-drawing": turnItems(t, "t7", completed, "Нарисуй маяк", "", "Рисую."),
		"thread-draft":   turnItems(t, "t8", completed, "Допиши главу о маяке", "", "Пишу."),
	}

	// The records of the saga the second member made.
	if _, err := memoryrepo.New(world.db).CreateAuthorMemory(world.ctx, memoryservice.CreateCommand{
		Write:     memoryservice.Write{Actor: memoryservice.Actor{UserID: second.id, ProjectID: opened.ProjectID}, Proof: second.proof},
		Draft:     memorydomain.Draft{Kind: memorydomain.KindPlan, Text: "Маяк погаснет в третьей книге."},
		ScopeHash: "memory-scope", OperationID: "author_memory.create.v1", KeyHash: "memory-key", RequestHash: "memory-request", At: talk.at,
	}); err != nil {
		t.Fatalf("a record of the memory of the second member: %v", err)
	}
	if _, err := materialsrepo.New(world.db, jobstore.New(world.db)).CreateAuthorMaterial(world.ctx, materialsservice.CreateCommand{
		Write:  materialsservice.Write{Actor: materialsservice.Actor{UserID: second.id, ProjectID: opened.ProjectID}, Proof: second.proof},
		Replay: materialsservice.Replay{ScopeHash: "material-scope", OperationID: "author_material.create.v1", KeyHash: "material-key", RequestHash: "material-request"},
		Draft:  materialsdomain.Draft{Name: "Ойвин", Kind: materialsdomain.KindCharacter, Description: "Смотритель маяка."},
		Key:    sagamemory.GlossaryKey("Ойвин"), At: talk.at,
	}); err != nil {
		t.Fatalf("a material of the second member: %v", err)
	}

	file := world.save(opened.ProjectID)
	archive := read(t, file)
	var turns []string
	if err := archive.EachChat(func(entry domain.ChatEntry, transcript domain.ChatTranscript) error {
		for _, turn := range transcript.Turns {
			turns = append(turns, fmt.Sprintf("%s: %s → %s [%s] %s", entry.Title, turn.Question, turn.Answer, turn.Attached, turn.Outcome))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	began := "Чат от " + theirsAt.Format("02.01.2006")
	want := []string{
		"Кто ждёт Мару: Кто ждёт Мару? → Тарен, [Застава ¶1](source:b1c1/1). [Набросок главы] answered",
		"Кто ждёт Мару: Кто ждёт Мару на заставе? → Тарен ждёт её третий день. [Набросок главы] answered",
		"Кто ждёт Мару: Сколько дней ждал Тарен? →  [] failed",
		began + ": Где стоит маяк? → На краю льдов. [] answered",
	}
	slices.Sort(turns)
	slices.Sort(want)
	if strings.Join(turns, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the turns of the file:\n%s\nwant\n%s", strings.Join(turns, "\n"), strings.Join(want, "\n"))
	}
	words := unpacked(t, file)
	for _, other := range []string{"Тайное имя второго", "Ответ второму", "Секрет второго", "Вопрос второго", "Только второй", "Чат второго",
		"Только по первой книге", "Нарисуй маяк", "Допиши главу", "Рисую.", "Пишу."} {
		if strings.Contains(words, other) {
			t.Fatalf("%q went into the file", other)
		}
	}
	// The turns of the other member: one in each chat of the saga and the two of the chats of the file it came from.
	if left := archive.Manifest.LeftOut.OthersTurns; left != 3+2 {
		t.Fatalf("the turns of other members = %d; want 5, the helpers not counted", left)
	}
	if !slices.ContainsFunc(archive.Memory, func(record domain.Memory) bool {
		return record.Body == "Маяк погаснет в третьей книге."
	}) ||
		!slices.ContainsFunc(archive.Memory, func(record domain.Memory) bool { return record.Body == "Запись 1 о Таренe." }) {
		t.Fatalf("the memory of the file = %+v; want the records of both members", archive.Memory)
	}
	if !slices.ContainsFunc(archive.Materials, func(material domain.Material) bool { return material.Name == "Ойвин" }) ||
		!slices.ContainsFunc(archive.Materials, func(material domain.Material) bool { return material.Name == "Северный маяк" }) {
		t.Fatalf("the materials of the file = %+v; want those of both members", archive.Materials)
	}
}

// unpacked is the text of every record of a saga file.
func unpacked(t *testing.T, file []byte) string {
	t.Helper()
	var words bytes.Buffer
	archive := read(t, file)
	if err := archive.EachChapter(func(chapter domain.Chapter) error {
		_, err := fmt.Fprintln(&words, chapter)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := archive.EachChat(func(entry domain.ChatEntry, transcript domain.ChatTranscript) error {
		_, err := fmt.Fprintln(&words, entry, transcript)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintln(&words, archive.Manifest, archive.Memory, archive.Materials, archive.Notes)
	return words.String()
}
