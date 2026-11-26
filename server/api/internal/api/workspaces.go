package api

import (
	"errors"
	"net/http"
	"strings"
)

// newWorkspace lets anyone create a workspace (unless an admin turned that
// off); they become its owner.
func (s *Server) newWorkspace(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.settings(r.Context()).AllowWorkspaceCreation && !p.IsAdmin {
		httpError(w, http.StatusForbidden, "Your admin turned off creating workspaces. Ask them to make one for you.")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		httpError(w, http.StatusBadRequest, "Give the workspace a name (up to 80 characters)")
		return
	}
	id, err := s.createWorkspace(r.Context(), in.Name, false, p.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "workspace.created", TargetType: "workspace", TargetID: id, TargetLabel: in.Name, WorkspaceID: id})
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "name": in.Name, "role": "owner"})
}

// setMember changes a member's role (workspace owners only).
func (s *Server) setMember(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.MemberRole != "owner" {
		httpError(w, http.StatusForbidden, "Only workspace owners can change roles")
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	uid := urlParam(r, "userId")
	var email string
	if err := s.db.QueryRow(r.Context(), `SELECT u.email FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id = $1 AND m.user_id = $2`,
		p.WorkspaceID, uid).Scan(&email); err != nil {
		httpError(w, http.StatusNotFound, "Not a member of this workspace")
		return
	}
	if err := s.setMemberRole(r.Context(), p.WorkspaceID, uid, in.Role); err != nil {
		if errors.Is(err, errBadRole) || errors.Is(err, errLastOwner) {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "member.role_set", TargetType: "user", TargetID: uid, TargetLabel: email, WorkspaceID: p.WorkspaceID,
		Details: map[string]any{"role": in.Role}})
	w.WriteHeader(http.StatusNoContent)
}

// removeMember removes someone from the workspace (owners), or lets a
// member leave.
func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	uid := urlParam(r, "userId")
	if uid != p.UserID && p.MemberRole != "owner" {
		httpError(w, http.StatusForbidden, "Only workspace owners can remove people")
		return
	}
	if err := s.keepAnOwner(r.Context(), p.WorkspaceID, uid); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var email string
	err := s.db.QueryRow(r.Context(), `
		DELETE FROM members m USING users u WHERE m.workspace_id = $1 AND m.user_id = $2 AND u.id = m.user_id
		RETURNING u.email`, p.WorkspaceID, uid).Scan(&email)
	if err != nil {
		httpError(w, http.StatusNotFound, "Not a member of this workspace")
		return
	}
	action := "member.removed"
	if uid == p.UserID {
		action = "member.left"
	}
	s.audit(r.Context(), r, p, auditEntry{Action: action, TargetType: "user", TargetID: uid, TargetLabel: email, WorkspaceID: p.WorkspaceID})
	w.WriteHeader(http.StatusNoContent)
}

// revokeInvite cancels a pending invite to this workspace (owners only).
func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.MemberRole != "owner" {
		httpError(w, http.StatusForbidden, "Only workspace owners can cancel invites")
		return
	}
	email := strings.ToLower(r.URL.Query().Get("email"))
	tag, err := s.db.Exec(r.Context(), `DELETE FROM invites WHERE workspace_id = $1 AND lower(email) = $2`, p.WorkspaceID, email)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpError(w, http.StatusNotFound, "No pending invite for "+email)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "invite.revoked", TargetType: "invite", TargetLabel: email, WorkspaceID: p.WorkspaceID})
	w.WriteHeader(http.StatusNoContent)
}
