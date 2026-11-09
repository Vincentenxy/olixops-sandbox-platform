package domain

import "time"

type Namespace struct {
	ID      string `json:"id"`
	OwnerID string `json:"ownerId"`
	Name    string `json:"name"`
}
type Workspace struct {
	AttachmentGeneration int64      `json:"attachmentGeneration"`
	ID                   string     `json:"id"`
	NamespaceID          string     `json:"namespaceId"`
	OwnerID              string     `json:"ownerId"`
	ProjectID            string     `json:"projectId"`
	Name                 string     `json:"name"`
	RequestKey           string     `json:"-"`
	State                string     `json:"state"`
	ClusterID            string     `json:"-"`
	KubeNamespace        string     `json:"-"`
	PVCName              string     `json:"pvcName"`
	PVCUID               string     `json:"pvcUid"`
	PVName               string     `json:"pvName"`
	StorageClass         string     `json:"storageClass"`
	RequestedBytes       int64      `json:"requestedBytes"`
	StoragePhase         string     `json:"storagePhase"`
	Message              string     `json:"message"`
	ActiveSandboxID      string     `json:"activeSandboxId"`
	ArchivedAt           *time.Time `json:"archivedAt"`
	LastObservedAt       *time.Time `json:"lastObservedAt"`
	CreateAt             time.Time  `json:"createAt"`
}
type Conversation struct {
	ID            string `json:"id"`
	NamespaceID   string `json:"namespaceId"`
	ProjectID     string `json:"projectId"`
	OwnerID       string `json:"ownerId"`
	IntegrationID string `json:"integrationId"`
	ExternalID    string `json:"externalId"`
	Title         string `json:"title"`
	WorkspaceID   string `json:"workspaceId"`
	Version       int64  `json:"version"`
}
type Binding struct {
	WorkspaceID string    `json:"workspaceId"`
	Version     int64     `json:"version"`
	Actor       string    `json:"actor"`
	CreateAt    time.Time `json:"createAt"`
}
