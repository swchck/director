package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// File represents a Directus file/asset.
type File struct {
	ID               string          `json:"id,omitempty"`
	Storage          string          `json:"storage,omitempty"`
	FilenameDisk     string          `json:"filename_disk,omitempty"`
	FilenameDownload string          `json:"filename_download,omitempty"`
	Title            string          `json:"title,omitempty"`
	Type             string          `json:"type,omitempty"`
	Folder           *string         `json:"folder,omitempty"`
	UploadedBy       string          `json:"uploaded_by,omitempty"`
	CreatedOn        string          `json:"created_on,omitempty"`
	ModifiedBy       string          `json:"modified_by,omitempty"`
	ModifiedOn       string          `json:"modified_on,omitempty"`
	Filesize         int64           `json:"filesize,omitempty"`
	Width            *int            `json:"width,omitempty"`
	Height           *int            `json:"height,omitempty"`
	Duration         *int            `json:"duration,omitempty"`
	Description      string          `json:"description,omitempty"`
	Tags             json.RawMessage `json:"tags,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
}

func (c *Client) ListFiles(ctx context.Context, opts ...QueryOption) ([]File, error) {
	return list[File](c, ctx, "files", opts)
}

func (c *Client) GetFile(ctx context.Context, id string) (*File, error) {
	return get[File](c, ctx, "files/"+id)
}

func (c *Client) UpdateFile(ctx context.Context, id string, file File) (*File, error) {
	return update[File](c, ctx, "files/"+id, file)
}

func (c *Client) DeleteFile(ctx context.Context, id string) error {
	return c.Delete(ctx, "files/"+id)
}

// ImportFileInput configures a file import from URL.
type ImportFileInput struct {
	URL  string `json:"url"`
	Data *File  `json:"data,omitempty"`
}

func (c *Client) ImportFile(ctx context.Context, input ImportFileInput) (*File, error) {
	raw, err := c.Post(ctx, "files/import", input)
	if err != nil {
		return nil, fmt.Errorf("directus: import file: %w", err)
	}

	var file File
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("directus: unmarshal imported file: %w", err)
	}

	return &file, nil
}

// AssetURL returns the URL for accessing a file asset with optional transformations.
func (c *Client) AssetURL(id string, key string) string {
	u := c.BaseURL() + "/assets/" + id
	if key != "" {
		u += "?key=" + key
	}

	return u
}
