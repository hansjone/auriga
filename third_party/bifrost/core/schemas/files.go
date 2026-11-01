package schemas

type S3StorageConfig struct {
	Bucket string `json:"bucket,omitempty"`
	Region string `json:"region,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}
type BifrostFileDeleteResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Deleted bool   `json:"deleted"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type FileStorageBackend string
type FileStorageConfig struct {
	S3  *S3StorageConfig  `json:"s3,omitempty"`
	GCS *GCSStorageConfig `json:"gcs,omitempty"`
}
type BifrostFileListRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	RawRequestBody []byte `json:"-"`

	Purpose FilePurpose `json:"purpose,omitempty"`

	Limit int     `json:"limit,omitempty"`
	After *string `json:"after,omitempty"`
	Order *string `json:"order,omitempty"`

	StorageConfig *FileStorageConfig `json:"storage_config,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostFileUploadResponse struct {
	ID            string      `json:"id"`
	Object        string      `json:"object,omitempty"`
	Bytes         int64       `json:"bytes"`
	CreatedAt     int64       `json:"created_at"`
	Filename      string      `json:"filename"`
	ContentType   string      `json:"content_type,omitempty"`
	Downloadable  *bool       `json:"downloadable,omitempty"`
	Purpose       FilePurpose `json:"purpose"`
	Status        FileStatus  `json:"status,omitempty"`
	StatusDetails *string     `json:"status_details,omitempty"`
	ExpiresAt     *int64      `json:"expires_at,omitempty"`

	StorageBackend FileStorageBackend `json:"storage_backend,omitempty"`
	StorageURI     string             `json:"storage_uri,omitempty"`

	UploadURL *string `json:"upload_url,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostFileDeleteRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`
	FileID   string        `json:"file_id"`

	RawRequestBody []byte `json:"-"`

	StorageConfig *FileStorageConfig `json:"storage_config,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type FileExpiresAfter struct {
	Anchor  string `json:"anchor"`
	Seconds int    `json:"seconds"`
}
type BifrostFileUploadRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	File        []byte      `json:"-"`
	Filename    string      `json:"filename"`
	Purpose     FilePurpose `json:"purpose"`
	ContentType *string     `json:"content_type,omitempty"`

	StorageConfig *FileStorageConfig `json:"storage_config,omitempty"`

	ExpiresAfter *FileExpiresAfter `json:"expires_after,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type FileStatus string
type FilePurpose string
type BifrostFileListResponse struct {
	Object  string       `json:"object,omitempty"`
	Data    []FileObject `json:"data"`
	HasMore bool         `json:"has_more,omitempty"`
	After   *string      `json:"after,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostFileRetrieveResponse struct {
	ID            string      `json:"id"`
	Object        string      `json:"object,omitempty"`
	Bytes         int64       `json:"bytes"`
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at,omitempty"`
	Filename      string      `json:"filename"`
	ContentType   string      `json:"content_type,omitempty"`
	Downloadable  *bool       `json:"downloadable,omitempty"`
	Purpose       FilePurpose `json:"purpose"`
	Status        FileStatus  `json:"status,omitempty"`
	StatusDetails *string     `json:"status_details,omitempty"`
	ExpiresAt     *int64      `json:"expires_at,omitempty"`

	StorageBackend FileStorageBackend `json:"storage_backend,omitempty"`
	StorageURI     string             `json:"storage_uri,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type BifrostFileContentRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`
	FileID   string        `json:"file_id"`

	RawRequestBody []byte `json:"-"`

	StorageConfig *FileStorageConfig `json:"storage_config,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type BifrostFileContentResponse struct {
	FileID      string `json:"file_id"`
	Content     []byte `json:"-"`
	ContentType string `json:"content_type,omitempty"`

	ExtraFields BifrostResponseExtraFields `json:"extra_fields"`
}
type FileObject struct {
	ID            string      `json:"id"`
	Object        string      `json:"object,omitempty"`
	Bytes         int64       `json:"bytes"`
	CreatedAt     int64       `json:"created_at"`
	UpdatedAt     int64       `json:"updated_at,omitempty"`
	Filename      string      `json:"filename"`
	ContentType   string      `json:"content_type,omitempty"`
	Downloadable  *bool       `json:"downloadable,omitempty"`
	Purpose       FilePurpose `json:"purpose"`
	Status        FileStatus  `json:"status,omitempty"`
	StatusDetails *string     `json:"status_details,omitempty"`
	ExpiresAt     *int64      `json:"expires_at,omitempty"`
}
type BifrostFileRetrieveRequest struct {
	Provider ModelProvider `json:"provider"`
	Model    *string       `json:"model"`

	RawRequestBody []byte `json:"-"`

	FileID string `json:"file_id"`

	StorageConfig *FileStorageConfig `json:"storage_config,omitempty"`

	ExtraParams map[string]interface{} `json:"-"`
}
type GCSStorageConfig struct {
	Bucket  string `json:"bucket,omitempty"`
	Project string `json:"project,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
}

const (
	FilePurposeBatch       FilePurpose = "batch"
	FilePurposeAssistants  FilePurpose = "assistants"
	FilePurposeFineTune    FilePurpose = "fine-tune"
	FilePurposeVision      FilePurpose = "vision"
	FilePurposeBatchOutput FilePurpose = "batch_output"
	FilePurposeUserData    FilePurpose = "user_data"
	FilePurposeResponses   FilePurpose = "responses"
	FilePurposeEvals       FilePurpose = "evals"
)

const (
	FileStatusUploaded      FileStatus = "uploaded"
	FileStatusProcessed     FileStatus = "processed"
	FileStatusProcessing    FileStatus = "processing"
	FileStatusPendingUpload FileStatus = "pending_upload"
	FileStatusError         FileStatus = "error"
	FileStatusDeleted       FileStatus = "deleted"
)

const (
	FileStorageAPI    FileStorageBackend = "api"
	FileStorageS3     FileStorageBackend = "s3"
	FileStorageGCS    FileStorageBackend = "gcs"
	FileStorageMemory FileStorageBackend = "memory"
)

func (request *BifrostFileListRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostFileRetrieveRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostFileDeleteRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}

func (request *BifrostFileContentRequest) GetRawRequestBody() []byte {
	return request.RawRequestBody
}
