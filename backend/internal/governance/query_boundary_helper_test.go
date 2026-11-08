package governance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

type QueryBoundaryCode string

const (
	QueryBoundaryMissingProject      QueryBoundaryCode = "missing_project_scope"
	QueryBoundaryUnregisteredActor   QueryBoundaryCode = "unregistered_actor_query"
	QueryBoundaryActorContractDrift  QueryBoundaryCode = "actor_query_contract_drift"
	QueryBoundaryActorReturnsContent QueryBoundaryCode = "actor_resolver_returns_content"
	QueryBoundaryInlineWorkerClaim   QueryBoundaryCode = "inline_worker_claim"
)

type QueryBoundaryFinding struct {
	Code   QueryBoundaryCode
	File   string
	Query  string
	Detail string
}

type namedSQLQuery struct {
	name    string
	command string
	body    string
}

var namedQueryMarker = regexp.MustCompile(`(?m)^-- name:\s*([A-Za-z][A-Za-z0-9_]*)\s+(:[a-z]+)\s*$`)

var tenantQueryTables = []string{
	"projects", "project_memberships", "project_invitations", "books", "chapters",
	"chapter_paragraphs", "jobs", "job_model_bindings", "chat_sessions",
	"chat_turns", "agent_workflow_inputs", "import_uploads", "import_job_requests",
	"deletion_tombstones", "deletion_target_tasks", "deletion_target_step_progress",
	"deletion_completion_receipts", "artifact_source_owners", "artifact_source_refs",
}

var productBodyColumns = []string{
	"title", "name", "description", "content", "plain_text", "markdown", "body_json",
	"progress_json", "result_json", "error_json", "source_file_name",
}

var actorQuerySQLContractDigests = map[ActorQuery]string{
	QueryListProjectsForActor:                          "eff07bece38bdccdd220b8208cfa138ed56a73425fadb29281dde269ade1aea3",
	QueryListProjectInvitationsForActor:                "b19a0d09c5874a879a55eb12eed8bf4388ef9e52ecd4a1312960a609c6a60875",
	QueryResolveBookAccessForActor:                     "eebb7f9b1471c85e025f6db776b636d39f7810f15bc8151d915ddef15d4a5d2f",
	QueryResolveChapterAccessForActor:                  "696df85df05cb6b8e176e850b65a07912a94b6ff11b347a3ee8dacb39881f34f",
	QueryResolveProjectInvitationMemberAccessForActor:  "db4b715a7f881169d457595de7641f07d13ab0b5831bb684816c51887b73051c",
	QueryResolveProjectInvitationInviteeAccessForActor: "7272c02be5b00d47959effc9d2304a801894c3f4fb623a223144ee217a998716",
	QueryResolveJobAccessForActor:                      "767e67f81acb5916b36de8ddbd0a0ed740acca733d816185a539337e3b0ddded",
}

func CheckQueryBoundarySQL(file, source string) []QueryBoundaryFinding {
	queries := splitNamedQueries(source)
	actorRegistry := make(map[string]ActorQueryRegistration, len(actorQueryRegistry))
	for _, registration := range ActorQueryRegistry() {
		actorRegistry[string(registration.Name)] = registration
	}

	var findings []QueryBoundaryFinding
	for _, query := range queries {
		normalized := normalizeQuerySQL(query.body)
		lower := strings.ToLower(normalized)

		if registration, ok := workerClaimRegistration(query.name); ok {
			call := "from " + registration.Schema + "." + registration.Function + "("
			if !strings.HasPrefix(lower, "select ") || !strings.Contains(lower, call) || touchesTenantTable(lower) {
				findings = append(findings, QueryBoundaryFinding{
					Code: QueryBoundaryInlineWorkerClaim, File: file, Query: query.name,
					Detail: "registered worker discovery must be one typed call to its fixed security-definer function",
				})
			}
			continue
		}

		if registration, ok := actorRegistry[query.name]; ok {
			expectedDigest, frozen := actorQuerySQLContractDigests[registration.Name]
			contractBody := query.command + "\n" + strings.TrimSpace(strings.ReplaceAll(query.body, "\r\n", "\n"))
			actualDigest := sha256.Sum256([]byte(contractBody))
			if !frozen || hex.EncodeToString(actualDigest[:]) != expectedDigest {
				findings = append(findings, QueryBoundaryFinding{
					Code: QueryBoundaryActorContractDrift, File: file, Query: query.name,
					Detail: "registered actor query differs from its reviewed projection, proof, or pagination contract",
				})
			}
			if !strings.Contains(lower, "sqlc.arg(actor_user_id)") {
				findings = append(findings, QueryBoundaryFinding{
					Code: QueryBoundaryUnregisteredActor, File: file, Query: query.name,
					Detail: "registered actor query does not bind the authenticated actor",
				})
			}
			if registration.Kind == ActorQueryResourceResolver && bodyProjectionContains(lower, productBodyColumns) {
				findings = append(findings, QueryBoundaryFinding{
					Code: QueryBoundaryActorReturnsContent, File: file, Query: query.name,
					Detail: "actor resource resolver may return only its owning identity tuple",
				})
			}
			continue
		}

		if strings.Contains(lower, "actor_user_id") {
			findings = append(findings, QueryBoundaryFinding{
				Code: QueryBoundaryUnregisteredActor, File: file, Query: query.name,
				Detail: "actor-only lookup is not in ActorQueryRegistry",
			})
		}
		if touchesTenantTable(lower) && !strings.Contains(lower, "sqlc.arg(project_id)") {
			findings = append(findings, QueryBoundaryFinding{
				Code: QueryBoundaryMissingProject, File: file, Query: query.name,
				Detail: "ordinary tenant query does not carry an explicit project_id argument",
			})
		}
	}
	return findings
}

func splitNamedQueries(source string) []namedSQLQuery {
	matches := namedQueryMarker.FindAllStringSubmatchIndex(source, -1)
	queries := make([]namedSQLQuery, 0, len(matches))
	for index, match := range matches {
		bodyEnd := len(source)
		if index+1 < len(matches) {
			bodyEnd = matches[index+1][0]
		}
		queries = append(queries, namedSQLQuery{
			name:    source[match[2]:match[3]],
			command: source[match[4]:match[5]],
			body:    source[match[1]:bodyEnd],
		})
	}
	return queries
}

func normalizeQuerySQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

func touchesTenantTable(lowerSQL string) bool {
	for _, table := range tenantQueryTables {
		for _, prefix := range []string{" from ", " join ", " update ", " into ", " delete from "} {
			if strings.Contains(" "+lowerSQL, prefix+table+" ") ||
				strings.Contains(" "+lowerSQL, prefix+table+"\n") {
				return true
			}
		}
	}
	return false
}

func bodyProjectionContains(lowerSQL string, forbidden []string) bool {
	selectIndex := strings.Index(lowerSQL, "select ")
	fromIndex := strings.Index(lowerSQL, " from ")
	if selectIndex == -1 || fromIndex == -1 || fromIndex <= selectIndex {
		return false
	}
	projection := lowerSQL[selectIndex+len("select ") : fromIndex]
	for _, column := range forbidden {
		matched, err := regexp.MatchString(`(^|[^a-z0-9_])`+regexp.QuoteMeta(column)+`([^a-z0-9_]|$)`, projection)
		if err != nil {
			panic(fmt.Sprintf("compile query-boundary column matcher: %v", err))
		}
		if matched {
			return true
		}
	}
	return false
}
