package schemas

type BifrostContainerRetrieveResponse struct {
	ID           string                 `json:"id"`
	Object       string                 `json:"object,omitempty"`
	Name         string                 `json:"name"`
	CreatedAt    int64                  `json:"created_at"`
	Status       ContainerStatus        `json:"status,omitempty"`
	ExpiresAfter *ContainerExpiresAfter `json:"expires_after,omitempty"`
	LastActiveAt *int64                 `json:"last_active_at,omitempty"`
	MemoryLimit  string                 `json:"memory_limit,omitempty"`
	Metadata     map[string]string      `json:"metadata,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerDeleteResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Deleted bool   `json:"deleted"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerFileDeleteResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Deleted bool   `json:"deleted"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerListResponse struct {
	Object  string            `json:"object,omitempty"`
	Data    []ContainerObject `json:"data"`
	FirstID *string           `json:"first_id,omitempty"`
	LastID  *string           `json:"last_id,omitempty"`
	HasMore bool              `json:"has_more,omitempty"`
	After   *string           `json:"after,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerFileListRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`

	Limit int     `json:"limit,omitempty"`
	After *string `json:"after,omitempty"`
	Order *string `json:"order,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type ContainerObject struct {
	ID           string                 `json:"id"`
	Object       string                 `json:"object,omitempty"`
	Name         string                 `json:"name"`
	CreatedAt    int64                  `json:"created_at"`
	Status       ContainerStatus        `json:"status,omitempty"`
	ExpiresAfter *ContainerExpiresAfter `json:"expires_after,omitempty"`
	LastActiveAt *int64                 `json:"last_active_at,omitempty"`
	MemoryLimit  string                 `json:"memory_limit,omitempty"`
	Metadata     map[string]string      `json:"metadata,omitempty"`
}
type BifrostContainerDeleteRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostContainerFileCreateRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`

	File   []byte  `json:"-"`
	FileID *string `json:"file_id,omitempty"`
	Path   *string `json:"file_path,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostContainerFileContentRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`
	FileID      string        `json:"file_id"`

	ExtraParams map[string]interface{} `json:"-"`
}
type ContainerStatus string
type BifrostContainerFileRetrieveResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object,omitempty"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	ContainerID string `json:"container_id"`
	Path        string `json:"path"`
	Source      string `json:"source"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerFileListResponse struct {
	Object  string                `json:"object,omitempty"`
	Data    []ContainerFileObject `json:"data"`
	FirstID *string               `json:"first_id,omitempty"`
	LastID  *string               `json:"last_id,omitempty"`
	HasMore bool                  `json:"has_more,omitempty"`
	After   *string               `json:"after,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerRetrieveRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostContainerFileDeleteRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`
	FileID      string        `json:"file_id"`

	ExtraParams map[string]interface{} `json:"-"`
}
type ContainerExpiresAfter struct {
	Anchor  string `json:"anchor"`
	Minutes int    `json:"minutes"`
}
type ContainerFileObject struct {
	ID          string `json:"id"`
	Object      string `json:"object,omitempty"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	ContainerID string `json:"container_id"`
	Path        string `json:"path"`
	Source      string `json:"source"`
}
type BifrostContainerListRequest struct {
	Provider ModelProvider `json:"provider"`

	Limit int     `json:"limit,omitempty"`
	After *string `json:"after,omitempty"`
	Order *string `json:"order,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostContainerFileCreateResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object,omitempty"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	ContainerID string `json:"container_id"`
	Path        string `json:"path"`
	Source      string `json:"source"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerFileContentResponse struct {
	Content     []byte `json:"content"`
	ContentType string `json:"content_type"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerCreateResponse struct {
	ID           string                 `json:"id"`
	Object       string                 `json:"object,omitempty"`
	Name         string                 `json:"name"`
	CreatedAt    int64                  `json:"created_at"`
	Status       ContainerStatus        `json:"status,omitempty"`
	ExpiresAfter *ContainerExpiresAfter `json:"expires_after,omitempty"`
	LastActiveAt *int64                 `json:"last_active_at,omitempty"`
	MemoryLimit  string                 `json:"memory_limit,omitempty"`
	Metadata     map[string]string      `json:"metadata,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostContainerCreateRequest struct {
	Provider ModelProvider `json:"provider"`

	Name string `json:"name"`

	ExpiresAfter *ContainerExpiresAfter `json:"expires_after,omitempty"`
	FileIDs      []string               `json:"file_ids,omitempty"`
	MemoryLimit  string                 `json:"memory_limit,omitempty"`
	Metadata     map[string]string      `json:"metadata,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostContainerFileRetrieveRequest struct {
	Provider    ModelProvider `json:"provider"`
	ContainerID string        `json:"container_id"`
	FileID      string        `json:"file_id"`

	ExtraParams map[string]interface{} `json:"-"`
}
