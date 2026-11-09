package domain

import "time"

type Project struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	Quota    int       `json:"quota"`
	CreateAt time.Time `json:"createAt"`
}
type Member struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}
type Sandbox struct {
	WorkspaceID string     `json:"workspaceId,omitempty"`
	ID          string     `json:"id"`
	ProjectID   string     `json:"projectId"`
	OwnerID     string     `json:"ownerId"`
	Name        string     `json:"name"`
	Image       string     `json:"image"`
	BackendID   string     `json:"backendId"`
	State       string     `json:"state"`
	RequestKey  string     `json:"requestKey"`
	TTL         int        `json:"ttl"`
	CreateAt    time.Time  `json:"createAt"`
	UpdateAt    time.Time  `json:"updateAt"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}
type Audit struct {
	RunContextID   string    `json:"runContextId,omitempty"`
	WorkspaceID    string    `json:"workspaceId,omitempty"`
	ConversationID string    `json:"conversationId,omitempty"`
	ID             int64     `json:"id"`
	ProjectID      string    `json:"projectId"`
	SandboxID      string    `json:"sandboxId"`
	Action         string    `json:"action"`
	Actor          string    `json:"actor"`
	Outcome        string    `json:"outcome"`
	CreateAt       time.Time `json:"createAt"`
}

func CanAccess(owner, actor, role string, admin bool) bool {
	return admin || role == "owner" || role == "maintainer" || (role == "member" && owner == actor)
}
func Terminal(state string) bool {
	return state == "Deleted" || state == "Failed" || state == "Expired"
}
