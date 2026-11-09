package domain

import "time"

type RunContext struct {
	ID                   string      `json:"id"`
	ProjectID            string      `json:"projectId"`
	OwnerID              string      `json:"ownerId"`
	Actor                string      `json:"actor"`
	ConversationID       string      `json:"conversationId"`
	WorkspaceID          string      `json:"workspaceId"`
	BindingVersion       int64       `json:"bindingVersion"`
	SandboxID            string      `json:"sandboxId"`
	AttachmentGeneration int64       `json:"attachmentGeneration"`
	Image                string      `json:"image"`
	TTL                  int         `json:"ttl"`
	State                string      `json:"state"`
	Reason               string      `json:"reason"`
	Message              string      `json:"message"`
	ReleasedAt           *time.Time  `json:"releasedAt"`
	CreateAt             time.Time   `json:"createAt"`
	Operation            Preparation `json:"operation"`
}
type Preparation struct {
	OwnsCreation  bool       `json:"-"`
	State         string     `json:"state"`
	Reason        string     `json:"reason"`
	Message       string     `json:"message"`
	Attempt       int        `json:"attempt"`
	Generation    int64      `json:"-"`
	LeaseOwner    string     `json:"-"`
	LeaseUntil    *time.Time `json:"-"`
	NextAttemptAt time.Time  `json:"nextAttemptAt"`
	CreateSentAt  *time.Time `json:"-"`
}
type ContinueRequest struct {
	ConversationID  string `json:"conversationId" binding:"required"`
	ExpectedVersion *int64 `json:"expectedVersion" binding:"required,min=0"`
	Image           string `json:"image" binding:"required"`
	TTL             int    `json:"ttl" binding:"min=60"`
	RequestKey      string `json:"requestKey" binding:"required,min=8,max=128"`
}

// Conflict carries a safe actionable explanation while HTTP code/msg remain standard.
type Conflict struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func (e *Conflict) Error() string          { return e.Reason + ": " + e.Message }
func Blocked(reason, message string) error { return &Conflict{reason, message} }
