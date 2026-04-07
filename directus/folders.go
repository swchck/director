package directus

import "context"

// Folder represents a Directus file/asset folder.
//
// Folders organize uploaded files within the Directus asset library.
// They are virtual — not mirrored in the storage adapter.
type Folder struct {
	ID     string  `json:"id,omitempty"`
	Name   string  `json:"name,omitempty"`
	Parent *string `json:"parent,omitempty"`
}

func (c *Client) ListFolders(ctx context.Context, opts ...QueryOption) ([]Folder, error) {
	return list[Folder](c, ctx, "folders", opts)
}

func (c *Client) GetFolder(ctx context.Context, id string) (*Folder, error) {
	return get[Folder](c, ctx, "folders/"+id)
}

// CreateFolder creates a file/asset folder.
//
// Example:
//
//	// Top-level folder.
//	folder, _ := dc.CreateFolder(ctx, directus.Folder{Name: "Photos"})
//
//	// Nested folder.
//	dc.CreateFolder(ctx, directus.Folder{Name: "Vacation", Parent: &folder.ID})
func (c *Client) CreateFolder(ctx context.Context, folder Folder) (*Folder, error) {
	return create[Folder](c, ctx, "folders", folder)
}

func (c *Client) UpdateFolder(ctx context.Context, id string, folder Folder) (*Folder, error) {
	return update[Folder](c, ctx, "folders/"+id, folder)
}

// DeleteFolder removes a file/asset folder.
// Files in this folder are moved to the root folder.
func (c *Client) DeleteFolder(ctx context.Context, id string) error {
	return c.Delete(ctx, "folders/"+id)
}
