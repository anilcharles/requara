package api

// SCIM 2.0 user provisioning (RFC 7643/7644) at /scim/v2, for Okta, Entra ID,
// OneLogin, JumpCloud and others. Authenticate with an admin token
// ("Authorization: Bearer sa_…").
//
// A provisioned user is an entry in the company directory: while active it
// lets that email sign in (whatever REQUARA_ALLOWED_DOMAINS says); when the
// directory deactivates or deletes it, the matching Requara accounts are
// deactivated and signed out. Instance admins (REQUARA_ADMIN_EMAILS) are
// managed on the server and are never deactivated by SCIM.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	scimUserSchema  = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimListSchema  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimErrorSchema = "urn:ietf:params:scim:api:messages:2.0:Error"
)

func scimJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/scim+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func scimError(w http.ResponseWriter, status int, scimType, detail string) {
	body := map[string]any{"schemas": []string{scimErrorSchema}, "status": strconv.Itoa(status), "detail": detail}
	if scimType != "" {
		body["scimType"] = scimType
	}
	scimJSON(w, status, body)
}

type dirUser struct {
	ID         string
	ExternalID string
	UserName   string
	Email      string
	Name       string
	GivenName  string
	FamilyName string
	Active     bool
	Created    time.Time
	Updated    time.Time
}

const dirCols = `id::text, external_id, user_name, email, name, given_name, family_name, active, created_at, updated_at`

func scanDir(row pgx.CollectableRow) (dirUser, error) {
	var u dirUser
	err := row.Scan(&u.ID, &u.ExternalID, &u.UserName, &u.Email, &u.Name, &u.GivenName, &u.FamilyName, &u.Active, &u.Created, &u.Updated)
	return u, err
}

func (s *Server) scimResource(u dirUser) map[string]any {
	formatted := u.Name
	if formatted == "" {
		formatted = strings.TrimSpace(u.GivenName + " " + u.FamilyName)
	}
	out := map[string]any{
		"schemas":     []string{scimUserSchema},
		"id":          u.ID,
		"userName":    u.UserName,
		"displayName": formatted,
		"name":        map[string]string{"givenName": u.GivenName, "familyName": u.FamilyName, "formatted": formatted},
		"emails":      []map[string]any{{"value": u.Email, "primary": true, "type": "work"}},
		"active":      u.Active,
		"meta": map[string]any{
			"resourceType": "User", "created": u.Created.UTC().Format(time.RFC3339), "lastModified": u.Updated.UTC().Format(time.RFC3339),
			"location": s.cfg.FrontendURL + "/scim/v2/Users/" + u.ID,
		},
	}
	if u.ExternalID != "" {
		out["externalId"] = u.ExternalID
	}
	return out
}

// scimBool accepts true/false and Entra ID's "True"/"False" strings.
func scimBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		b, err := strconv.ParseBool(strings.ToLower(x))
		return b, err == nil
	}
	return false, false
}

// applyUser copies SCIM attributes onto u. path is "" for a whole resource.
func applyUser(u *dirUser, path string, v any) {
	str := func(x any) string { s, _ := x.(string); return strings.TrimSpace(s) }
	switch strings.ToLower(path) {
	case "":
		m, _ := v.(map[string]any)
		for k, val := range m {
			if k != "schemas" && k != "id" && k != "meta" {
				applyUser(u, k, val)
			}
		}
	case "username":
		u.UserName = str(v)
	case "externalid":
		u.ExternalID = str(v)
	case "displayname":
		u.Name = str(v)
	case "active":
		if b, ok := scimBool(v); ok {
			u.Active = b
		}
	case "name":
		m, _ := v.(map[string]any)
		for k, val := range m {
			applyUser(u, "name."+k, val)
		}
	case "name.givenname":
		u.GivenName = str(v)
	case "name.familyname":
		u.FamilyName = str(v)
	case "name.formatted":
		if u.Name == "" {
			u.Name = str(v)
		}
	case "emails", `emails[type eq "work"].value`, "emails[primary eq true].value":
		switch x := v.(type) {
		case string:
			u.Email = strings.TrimSpace(x)
		case []any:
			for i, e := range x {
				m, _ := e.(map[string]any)
				if p, _ := scimBool(m["primary"]); p || i == 0 {
					if val := str(m["value"]); val != "" {
						u.Email = val
					}
				}
			}
		}
	}
}

func (u *dirUser) normalize() error {
	u.UserName = strings.TrimSpace(u.UserName)
	if u.UserName == "" {
		return errors.New("userName is required")
	}
	if u.Email == "" && strings.Contains(u.UserName, "@") {
		u.Email = u.UserName
	}
	u.Email = strings.ToLower(strings.TrimSpace(u.Email))
	if !strings.Contains(u.Email, "@") {
		return errors.New("an email address (emails or an email userName) is required")
	}
	return nil
}

// syncAccounts applies a directory entry to the Requara accounts with its
// email: deactivated entries deactivate and sign out those accounts.
func (s *Server) syncAccounts(ctx context.Context, r *http.Request, p *Principal, u dirUser, deleted bool) {
	if !u.Active || deleted {
		rows, err := s.db.Query(ctx, `
			UPDATE users SET disabled_at = COALESCE(disabled_at, now())
			WHERE lower(email) = $1 AND NOT is_admin AND disabled_at IS NULL RETURNING id`, u.Email)
		if err != nil {
			return
		}
		ids, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, id := range ids {
			_, _ = s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
			s.audit(ctx, r, p, auditEntry{Action: "user.deactivated", TargetType: "user", TargetID: id, TargetLabel: u.Email, Details: map[string]any{"by": "scim"}})
		}
		return
	}
	rows, err := s.db.Query(ctx, `UPDATE users SET disabled_at = NULL WHERE lower(email) = $1 AND disabled_at IS NOT NULL RETURNING id`, u.Email)
	if err != nil {
		return
	}
	ids, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, id := range ids {
		s.audit(ctx, r, p, auditEntry{Action: "user.reactivated", TargetType: "user", TargetID: id, TargetLabel: u.Email, Details: map[string]any{"by": "scim"}})
	}
}

func (s *Server) scimGet(ctx context.Context, id string) (dirUser, error) {
	rows, err := s.db.Query(ctx, `SELECT `+dirCols+` FROM directory_users WHERE id::text = $1`, id)
	if err != nil {
		return dirUser{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanDir)
}

var scimFilterRE = regexp.MustCompile(`(?i)^\s*([a-z.]+)\s+eq\s+"((?:[^"\\]|\\.)*)"\s*$`)

func (s *Server) scimListUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	start, _ := strconv.Atoi(q.Get("startIndex"))
	if start < 1 {
		start = 1
	}
	count, err := strconv.Atoi(q.Get("count"))
	if err != nil || count < 0 || count > 200 {
		count = 100
	}
	where, arg := "true", ""
	if f := q.Get("filter"); f != "" {
		m := scimFilterRE.FindStringSubmatch(f)
		if m == nil {
			scimError(w, http.StatusBadRequest, "invalidFilter", "Only `<attribute> eq \"value\"` filters are supported")
			return
		}
		arg = strings.ReplaceAll(m[2], `\"`, `"`)
		switch strings.ToLower(m[1]) {
		case "username":
			where = "lower(user_name) = lower($1)"
		case "externalid":
			where = "external_id = $1"
		case "emails.value", "emails", "email":
			where = "lower(email) = lower($1)"
		case "id":
			where = "id::text = $1"
		default:
			scimError(w, http.StatusBadRequest, "invalidFilter", "Can't filter on "+m[1])
			return
		}
	}
	args := []any{}
	if arg != "" || where != "true" {
		args = append(args, arg)
	}
	ctx := r.Context()
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM directory_users WHERE `+where, args...).Scan(&total); err != nil {
		serverError(w, err)
		return
	}
	rows, err := s.db.Query(ctx, `SELECT `+dirCols+` FROM directory_users WHERE `+where+
		` ORDER BY created_at, id LIMIT `+strconv.Itoa(count)+` OFFSET `+strconv.Itoa(start-1), args...)
	if err != nil {
		serverError(w, err)
		return
	}
	users, err := pgx.CollectRows(rows, scanDir)
	if err != nil {
		serverError(w, err)
		return
	}
	res := []map[string]any{}
	for _, u := range users {
		res = append(res, s.scimResource(u))
	}
	scimJSON(w, http.StatusOK, map[string]any{
		"schemas": []string{scimListSchema}, "totalResults": total, "startIndex": start, "itemsPerPage": len(res), "Resources": res,
	})
}

func (s *Server) scimGetUser(w http.ResponseWriter, r *http.Request) {
	u, err := s.scimGet(r.Context(), urlParam(r, "id"))
	if err != nil {
		scimError(w, http.StatusNotFound, "", "User not found")
		return
	}
	scimJSON(w, http.StatusOK, s.scimResource(u))
}

func readSCIM(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var in map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		scimError(w, http.StatusBadRequest, "invalidSyntax", "Invalid JSON: "+err.Error())
		return nil, false
	}
	return in, true
}

func (s *Server) scimCreateUser(w http.ResponseWriter, r *http.Request) {
	in, ok := readSCIM(w, r)
	if !ok {
		return
	}
	u := dirUser{Active: true}
	applyUser(&u, "", in)
	if err := u.normalize(); err != nil {
		scimError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	ctx := r.Context()
	rows, err := s.db.Query(ctx, `
		INSERT INTO directory_users (external_id, user_name, email, name, given_name, family_name, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING RETURNING `+dirCols,
		u.ExternalID, u.UserName, u.Email, u.Name, u.GivenName, u.FamilyName, u.Active)
	if err != nil {
		serverError(w, err)
		return
	}
	created, err := pgx.CollectExactlyOneRow(rows, scanDir)
	if err != nil {
		scimError(w, http.StatusConflict, "uniqueness", "A user with userName "+u.UserName+" already exists")
		return
	}
	p := principal(r)
	s.audit(ctx, r, p, auditEntry{Action: "scim.user_created", TargetType: "directory_user", TargetID: created.ID, TargetLabel: created.Email,
		Details: map[string]any{"active": created.Active}})
	s.syncAccounts(ctx, r, p, created, false)
	scimJSON(w, http.StatusCreated, s.scimResource(created))
}

func (s *Server) scimSave(w http.ResponseWriter, r *http.Request, u dirUser, before dirUser) {
	if err := u.normalize(); err != nil {
		scimError(w, http.StatusBadRequest, "invalidValue", err.Error())
		return
	}
	ctx := r.Context()
	rows, err := s.db.Query(ctx, `
		UPDATE directory_users SET external_id=$2, user_name=$3, email=$4, name=$5, given_name=$6, family_name=$7, active=$8, updated_at=now()
		WHERE id::text = $1 RETURNING `+dirCols, u.ID, u.ExternalID, u.UserName, u.Email, u.Name, u.GivenName, u.FamilyName, u.Active)
	if err != nil {
		scimError(w, http.StatusConflict, "uniqueness", "userName "+u.UserName+" is taken")
		return
	}
	saved, err := pgx.CollectExactlyOneRow(rows, scanDir)
	if err != nil {
		scimError(w, http.StatusNotFound, "", "User not found")
		return
	}
	p := principal(r)
	s.audit(ctx, r, p, auditEntry{Action: "scim.user_updated", TargetType: "directory_user", TargetID: saved.ID, TargetLabel: saved.Email,
		Details: map[string]any{"active": saved.Active}})
	if saved.Active != before.Active || saved.Email != before.Email {
		if saved.Email != before.Email && saved.Active {
			before.Active = false
			s.syncAccounts(ctx, r, p, before, false) // the old address no longer belongs to the directory
		}
		s.syncAccounts(ctx, r, p, saved, false)
	}
	scimJSON(w, http.StatusOK, s.scimResource(saved))
}

func (s *Server) scimReplaceUser(w http.ResponseWriter, r *http.Request) {
	before, err := s.scimGet(r.Context(), urlParam(r, "id"))
	if err != nil {
		scimError(w, http.StatusNotFound, "", "User not found")
		return
	}
	in, ok := readSCIM(w, r)
	if !ok {
		return
	}
	u := dirUser{ID: before.ID, Active: true}
	applyUser(&u, "", in)
	s.scimSave(w, r, u, before)
}

func (s *Server) scimPatchUser(w http.ResponseWriter, r *http.Request) {
	before, err := s.scimGet(r.Context(), urlParam(r, "id"))
	if err != nil {
		scimError(w, http.StatusNotFound, "", "User not found")
		return
	}
	var in struct {
		Operations []struct {
			Op    string `json:"op"`
			Path  string `json:"path"`
			Value any    `json:"value"`
		} `json:"Operations"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		scimError(w, http.StatusBadRequest, "invalidSyntax", "Invalid JSON: "+err.Error())
		return
	}
	u := before
	for _, op := range in.Operations {
		switch strings.ToLower(op.Op) {
		case "replace", "add":
			applyUser(&u, op.Path, op.Value)
		case "remove":
			// Removing attributes Requara needs (userName, email) isn't allowed; names can go.
			switch strings.ToLower(op.Path) {
			case "name.givenname":
				u.GivenName = ""
			case "name.familyname":
				u.FamilyName = ""
			case "displayname":
				u.Name = ""
			case "externalid":
				u.ExternalID = ""
			}
		default:
			scimError(w, http.StatusBadRequest, "invalidSyntax", "Unknown op "+op.Op)
			return
		}
	}
	s.scimSave(w, r, u, before)
}

func (s *Server) scimDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.db.Query(ctx, `DELETE FROM directory_users WHERE id::text = $1 RETURNING `+dirCols, urlParam(r, "id"))
	if err != nil {
		serverError(w, err)
		return
	}
	u, err := pgx.CollectExactlyOneRow(rows, scanDir)
	if err != nil {
		scimError(w, http.StatusNotFound, "", "User not found")
		return
	}
	p := principal(r)
	s.audit(ctx, r, p, auditEntry{Action: "scim.user_deleted", TargetType: "directory_user", TargetID: u.ID, TargetLabel: u.Email})
	s.syncAccounts(ctx, r, p, u, true)
	w.WriteHeader(http.StatusNoContent)
}

// Groups aren't provisioned: workspace membership is managed in Requara.
func (s *Server) scimGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		scimJSON(w, http.StatusOK, map[string]any{"schemas": []string{scimListSchema}, "totalResults": 0, "startIndex": 1, "itemsPerPage": 0, "Resources": []any{}})
		return
	}
	scimError(w, http.StatusNotImplemented, "", "Requara provisions users only; manage workspace membership in Requara")
}

func (s *Server) scimServiceProviderConfig(w http.ResponseWriter, _ *http.Request) {
	scimJSON(w, http.StatusOK, map[string]any{
		"schemas":          []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"documentationUri": "https://github.com/anilcharles/requara/blob/main/server/deploy/README.md#scim-provisioning",
		"patch":            map[string]bool{"supported": true},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":           map[string]any{"supported": true, "maxResults": 200},
		"changePassword":   map[string]bool{"supported": false},
		"sort":             map[string]bool{"supported": false},
		"etag":             map[string]bool{"supported": false},
		"authenticationSchemes": []map[string]any{{
			"type": "oauthbearertoken", "name": "Admin token", "primary": true,
			"description": "An admin token from the Requara admin console (Bearer sa_…)",
		}},
	})
}

func (s *Server) scimResourceTypes(w http.ResponseWriter, _ *http.Request) {
	scimJSON(w, http.StatusOK, map[string]any{
		"schemas": []string{scimListSchema}, "totalResults": 1, "Resources": []map[string]any{{
			"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"},
			"id":      "User", "name": "User", "endpoint": "/Users", "schema": scimUserSchema,
		}},
	})
}

// adminDirectory lists provisioned users for the admin console.
func (s *Server) adminDirectory(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT `+dirCols+` FROM directory_users ORDER BY lower(user_name) LIMIT 2000`)
	if err != nil {
		serverError(w, err)
		return
	}
	users, err := pgx.CollectRows(rows, scanDir)
	if err != nil {
		serverError(w, err)
		return
	}
	out := []map[string]any{}
	for _, u := range users {
		out = append(out, map[string]any{"id": u.ID, "userName": u.UserName, "email": u.Email, "name": strings.TrimSpace(firstNonEmpty(u.Name, u.GivenName+" "+u.FamilyName)),
			"active": u.Active, "externalId": u.ExternalID, "updatedAt": u.Updated})
	}
	writeJSON(w, http.StatusOK, out)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
