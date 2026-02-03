package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/google/uuid"
	"github.com/studojo/control-plane/internal/auth"
)

// UploadFileRequest JSON body for POST /v1/humanizer/upload-file.
type UploadFileRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
}

// FileUploadResponse JSON response for POST /v1/humanizer/upload-file.
type FileUploadResponse struct {
	FileURL     string `json:"file_url"`      // Blob storage URL with read SAS token (for worker)
	UploadSASURL string `json:"upload_sas_url"` // SAS URL for direct frontend upload
	BlobName    string `json:"blob_name"`     // Blob name to use for upload
	ContainerName string `json:"container_name"` // Container name
}

// HandleUploadHumanizerFile handles POST /v1/humanizer/upload-file.
// Returns SAS URLs for direct frontend upload to Azure Blob Storage.
func (h *Handler) HandleUploadHumanizerFile(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		WriteError(w, http.StatusUnauthorized, ErrUnauthorized, "unauthorized")
		return
	}

	var req UploadFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusUnprocessableEntity, ErrValidationFailed, "invalid JSON body")
		return
	}

	// Validate file type
	if !strings.HasSuffix(strings.ToLower(req.Filename), ".docx") {
		WriteError(w, http.StatusBadRequest, ErrValidationFailed, "only .docx files are allowed")
		return
	}

	// Get blob storage credentials from environment
	accountName := os.Getenv("AZURE_STORAGE_ACCOUNT_NAME")
	accountKey := os.Getenv("AZURE_STORAGE_ACCOUNT_KEY")
	containerName := "humanizer-temp" // Temporary uploads container

	if accountName == "" || accountKey == "" {
		slog.Error("blob storage credentials not configured")
		WriteError(w, http.StatusInternalServerError, ErrInternal, "storage not configured")
		return
	}

	// Create blob client
	cred, err := azblob.NewSharedKeyCredential(accountName, accountKey)
	if err != nil {
		slog.Error("create blob credential failed", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "storage configuration error")
		return
	}

	containerURL := fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, containerName)
	containerClient, err := container.NewClientWithSharedKeyCredential(containerURL, cred, nil)
	if err != nil {
		slog.Error("create container client failed", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "storage configuration error")
		return
	}

	// Ensure container exists
	_, err = containerClient.Create(context.Background(), &container.CreateOptions{})
	if err != nil {
		// Ignore if container already exists
		if !strings.Contains(err.Error(), "ContainerAlreadyExists") {
			slog.Warn("container create check failed", "error", err)
		}
	}

	// Generate unique blob name: user_id/timestamp-uuid/filename
	timestamp := time.Now().UTC().Format("20060102-150405")
	blobID := uuid.New().String()[:8]
	blobName := fmt.Sprintf("%s/%s-%s/%s", userID, timestamp, blobID, req.Filename)

	blobClient := containerClient.NewBlockBlobClient(blobName)

	// Generate SAS token for direct frontend upload (write permission, 1 hour expiry)
	uploadPermissions := sas.BlobPermissions{Write: true, Create: true}
	uploadSAS, err := sas.BlobSignatureValues{
		Protocol:      sas.ProtocolHTTPS,
		StartTime:     time.Now().UTC().Add(-5 * time.Minute), // Account for clock skew
		ExpiryTime:    time.Now().UTC().Add(1 * time.Hour),     // 1 hour for upload
		Permissions:   uploadPermissions.String(),
		ContainerName: containerName,
		BlobName:      blobName,
	}.SignWithSharedKey(cred)
	if err != nil {
		slog.Error("generate upload SAS token failed", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to generate upload URL")
		return
	}

	uploadSASURL := fmt.Sprintf("%s?%s", blobClient.URL(), uploadSAS.Encode())

	// Generate read SAS token for worker access (24 hour expiration)
	readPermissions := sas.BlobPermissions{Read: true}
	readSAS, err := sas.BlobSignatureValues{
		Protocol:      sas.ProtocolHTTPS,
		StartTime:     time.Now().UTC().Add(-5 * time.Minute),
		ExpiryTime:    time.Now().UTC().Add(24 * time.Hour),
		Permissions:   readPermissions.String(),
		ContainerName: containerName,
		BlobName:      blobName,
	}.SignWithSharedKey(cred)
	if err != nil {
		slog.Error("generate read SAS token failed", "error", err)
		WriteError(w, http.StatusInternalServerError, ErrInternal, "failed to generate read URL")
		return
	}

	// Return read URL for worker (file_url) and upload URL for frontend (upload_sas_url)
	blobURL := blobClient.URL()
	fileURL := fmt.Sprintf("%s?%s", blobURL, readSAS.Encode())

	WriteJSON(w, http.StatusOK, FileUploadResponse{
		FileURL:      fileURL,      // For worker to download
		UploadSASURL: uploadSASURL, // For frontend to upload directly
		BlobName:     blobName,
		ContainerName: containerName,
	})
}
