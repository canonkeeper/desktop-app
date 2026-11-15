// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	domain "canonkeeper/backend/internal/domain/accountlink"
	"canonkeeper/backend/internal/httpapi"
	service "canonkeeper/backend/internal/services/accountlink"
)

// Account are the use cases of the link of the app with an account of the site (specs/2026-11-13-desk-account.md).
type Account struct {
	Start interface {
		Execute(ctx context.Context, chooseHelper bool) (service.State, error)
	}
	Cancel interface{ Execute(context.Context) }
	Read   interface {
		Execute(context.Context, bool) service.State
	}
	Unlink interface {
		Execute(context.Context) service.Unlinked
	}
}

type accountLinkJSON struct {
	Address   string `json:"address"`
	ExpiresAt string `json:"expiresAt"`
}

type accountTierJSON struct {
	Code  string  `json:"code"`
	Title string  `json:"title"`
	Until *string `json:"until"`
}

type accountCounterJSON struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

type accountInfoJSON struct {
	Name      string              `json:"name"`
	Email     string              `json:"email"`
	Tier      accountTierJSON     `json:"tier"`
	Questions *accountCounterJSON `json:"questions"`
	Reading   accountCounterJSON  `json:"reading"`
	At        string              `json:"at"`
}

type accountPagesJSON struct {
	Tariffs string `json:"tariffs"`
	Apps    string `json:"apps"`
}

// accountJSON is DesktopAccount of the contract.
type accountJSON struct {
	State       string           `json:"state"`
	Link        *accountLinkJSON `json:"link"`
	Failure     *string          `json:"failure"`
	Account     *accountInfoJSON `json:"account"`
	Unreachable bool             `json:"unreachable"`
	Computer    string           `json:"computer"`
	PassStore   string           `json:"passStore"`
	Pages       accountPagesJSON `json:"pages"`
}

// linkStartJSON is DesktopAccountLinkStart of the contract: the intent of the author (specs/2026-11-15-desk-account-link-web.md
// AW3.2).
type linkStartJSON struct {
	ChooseHelper *bool `json:"chooseHelper"`
}

type unlinkedJSON struct {
	SiteUnlinked bool `json:"siteUnlinked"`
	PassRemoved  bool `json:"passRemoved"`
}

// The codes of the refusals of the link.
const (
	codeLinkUnavailable = "account.link_unavailable"
	codeLinkUnsupported = "account.link_unsupported"
)

// account is getAccount (AC2.1): the state of the link; fresh=true asks the site now.
func (handlers Handlers) account(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	query := r.URL.Query()
	fresh := query.Get("fresh")
	if len(query) > 1 || len(query) == 1 && len(query["fresh"]) != 1 || fresh != "" && fresh != "true" && fresh != "false" {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid query", "validation.failed")
		return
	}
	writeAccount(w, http.StatusOK, handlers.Account.Read.Execute(r.Context(), fresh == "true"))
}

// startLink is startAccountLink (AC1.1): a link that may not start now or that the site cannot return to is 409 and
// changes nothing. The optional body carries the intent to choose the helper of the account; without a body there is
// none, and a body that is not the object of the operation starts nothing (specs/2026-11-15-desk-account-link-web.md AW3.2).
func (handlers Handlers) startLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	chooseHelper, ok := linkIntent(r)
	if !ok {
		httpapi.WriteProblem(w, "", http.StatusBadRequest, "Bad Request", "invalid request body", "validation.failed")
		return
	}
	state, err := handlers.Account.Start.Execute(r.Context(), chooseHelper)
	switch {
	case errors.Is(err, domain.ErrNotNow):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "a link does not start now", codeLinkUnavailable)
	case errors.Is(err, domain.ErrCannotStart):
		httpapi.WriteProblem(w, "", http.StatusConflict, "Conflict", "this build cannot be linked", codeLinkUnsupported)
	case err != nil:
		unexpected(w, r, err)
	default:
		writeAccount(w, http.StatusOK, state)
	}
}

// linkIntent reads the optional body of startAccountLink: an empty one — no intent.
func linkIntent(r *http.Request) (bool, bool) {
	raw, err := io.ReadAll(httpapi.JSONBody(r))
	if err != nil {
		return false, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return false, true
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var request linkStartJSON
	if _, ok := decode(r, &request); !ok || request.ChooseHelper == nil {
		return false, false
	}
	return *request.ChooseHelper, true
}

// cancelLink is cancelAccountLink: the waiting ends.
func (handlers Handlers) cancelLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	handlers.Account.Cancel.Execute(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// unlinkAccount is unlinkAccount (AC2.4): whether the site heard it and whether the pass went from the computer.
func (handlers Handlers) unlinkAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !handlers.signedIn(w, r) {
		return
	}
	unlinked := handlers.Account.Unlink.Execute(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(unlinkedJSON{SiteUnlinked: unlinked.Site, PassRemoved: unlinked.Computer})
}

func writeAccount(w http.ResponseWriter, status int, state service.State) {
	response := accountJSON{
		State: string(state.Phase), Unreachable: state.Unreachable, Computer: state.Computer, PassStore: string(state.Store),
		Pages: accountPagesJSON{Tariffs: state.Pages.Tariffs, Apps: state.Pages.Apps},
	}
	if waiting := state.Waiting; waiting != nil {
		response.Link = &accountLinkJSON{Address: waiting.Address, ExpiresAt: moment(waiting.Expires)}
	}
	if state.Failure != "" {
		failure := string(state.Failure)
		response.Failure = &failure
	}
	if account := state.Account; account != nil {
		info := &accountInfoJSON{
			Name: account.Name, Email: account.Email,
			Tier:    accountTierJSON{Code: account.Tier.Code, Title: account.Tier.Title},
			Reading: accountCounterJSON{Used: account.Reading.Used, Limit: account.Reading.Limit},
			At:      moment(state.At),
		}
		if until := account.Tier.Until; until != nil {
			at := moment(*until)
			info.Tier.Until = &at
		}
		if account.Quota {
			info.Questions = &accountCounterJSON{Used: account.Questions.Used, Limit: account.Questions.Limit}
		}
		response.Account = info
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func moment(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
