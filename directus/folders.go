package directus

import "context"

// Folder represents a Directus file/asset folder. Folders are virtual: they group
// uploads in the asset library and are not mirrored in the storage adapter.
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

// CreateFolder creates a file/asset folder, nested when Parent is set.
func (c *Client) CreateFolder(ctx context.Context, folder Folder) (*Folder, error) {
	return create[Folder](c, ctx, "folders", folder)
}

func (c *Client) UpdateFolder(ctx context.Context, id string, folder Folder) (*Folder, error) {
	return update[Folder](c, ctx, "folders/"+id, folder)
}

// DeleteFolder removes a file/asset folder, moving its files to the root folder.
func (c *Client) DeleteFolder(ctx context.Context, id string) error {
	return c.Delete(ctx, "folders/"+id)
}
