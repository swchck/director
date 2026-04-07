package directus

import (
	"context"
	"encoding/json"
	"fmt"
)

// Role represents a Directus role.
type Role struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Icon        string   `json:"icon,omitempty"`
	Description string   `json:"description,omitempty"`
	Parent      *string  `json:"parent,omitempty"`
	Policies    []string `json:"policies,omitempty"`
	Users       []string `json:"users,omitempty"`
}

// ListRoles returns all roles.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	raw, err := c.Get(ctx, "roles", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: list roles: %w", err)
	}

	var roles []Role
	if err := json.Unmarshal(raw, &roles); err != nil {
		return nil, fmt.Errorf("directus: unmarshal roles: %w", err)
	}

	return roles, nil
}

// GetRole returns a role by ID.
func (c *Client) GetRole(ctx context.Context, id string) (*Role, error) {
	raw, err := c.Get(ctx, "roles/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get role %s: %w", id, err)
	}

	var role Role
	if err := json.Unmarshal(raw, &role); err != nil {
		return nil, fmt.Errorf("directus: unmarshal role: %w", err)
	}

	return &role, nil
}

// CreateRole creates a new role.
func (c *Client) CreateRole(ctx context.Context, role Role) (*Role, error) {
	raw, err := c.Post(ctx, "roles", role)
	if err != nil {
		return nil, fmt.Errorf("directus: create role: %w", err)
	}

	var created Role
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal created role: %w", err)
	}

	return &created, nil
}

// UpdateRole updates an existing role.
func (c *Client) UpdateRole(ctx context.Context, id string, role Role) (*Role, error) {
	raw, err := c.Patch(ctx, "roles/"+id, role)
	if err != nil {
		return nil, fmt.Errorf("directus: update role %s: %w", id, err)
	}

	var updated Role
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated role: %w", err)
	}

	return &updated, nil
}

// DeleteRole removes a role.
func (c *Client) DeleteRole(ctx context.Context, id string) error {
	return c.Delete(ctx, "roles/"+id)
}

// Policy represents a Directus access policy.
type Policy struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	AdminAccess bool   `json:"admin_access"`
	AppAccess   bool   `json:"app_access"`
	EnforceTFA  bool   `json:"enforce_tfa,omitempty"`
	IPAccess    string `json:"ip_access,omitempty"`
}

// ListPolicies returns all policies.
func (c *Client) ListPolicies(ctx context.Context) ([]Policy, error) {
	raw, err := c.Get(ctx, "policies", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: list policies: %w", err)
	}

	var policies []Policy
	if err := json.Unmarshal(raw, &policies); err != nil {
		return nil, fmt.Errorf("directus: unmarshal policies: %w", err)
	}

	return policies, nil
}

// GetPolicy returns a policy by ID.
func (c *Client) GetPolicy(ctx context.Context, id string) (*Policy, error) {
	raw, err := c.Get(ctx, "policies/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get policy %s: %w", id, err)
	}

	var policy Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, fmt.Errorf("directus: unmarshal policy: %w", err)
	}

	return &policy, nil
}

// CreatePolicy creates a new policy.
func (c *Client) CreatePolicy(ctx context.Context, policy Policy) (*Policy, error) {
	raw, err := c.Post(ctx, "policies", policy)
	if err != nil {
		return nil, fmt.Errorf("directus: create policy: %w", err)
	}

	var created Policy
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal created policy: %w", err)
	}

	return &created, nil
}

// UpdatePolicy updates an existing policy.
func (c *Client) UpdatePolicy(ctx context.Context, id string, policy Policy) (*Policy, error) {
	raw, err := c.Patch(ctx, "policies/"+id, policy)
	if err != nil {
		return nil, fmt.Errorf("directus: update policy %s: %w", id, err)
	}

	var updated Policy
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated policy: %w", err)
	}

	return &updated, nil
}

// DeletePolicy removes a policy.
func (c *Client) DeletePolicy(ctx context.Context, id string) error {
	return c.Delete(ctx, "policies/"+id)
}

// GrantAdminAccess finds the admin policy by name and sets admin_access=true.
// This is a convenience for Directus 11 where the bootstrap may not set admin_access
// on the Administrator policy.
func (c *Client) GrantAdminAccess(ctx context.Context) error {
	policies, err := c.ListPolicies(ctx)
	if err != nil {
		return fmt.Errorf("directus: grant admin: %w", err)
	}

	for _, p := range policies {
		if p.Name == "Administrator" && !p.AdminAccess {
			_, err := c.UpdatePolicy(ctx, p.ID, Policy{
				Name:        p.Name,
				AdminAccess: true,
				AppAccess:   true,
			})
			if err != nil {
				return fmt.Errorf("directus: update admin policy: %w", err)
			}

			return nil
		}
	}

	return nil
}

// PermissionAction is the type of operation a permission grants.
type PermissionAction string

const (
	ActionCreate PermissionAction = "create"
	ActionRead   PermissionAction = "read"
	ActionUpdate PermissionAction = "update"
	ActionDelete PermissionAction = "delete"
)

// Permission represents a Directus permission rule.
type Permission struct {
	ID          int              `json:"id,omitempty"`
	Collection  string           `json:"collection"`
	Action      PermissionAction `json:"action"`
	Policy      string           `json:"policy"`
	Fields      []string         `json:"fields,omitempty"`
	Permissions map[string]any   `json:"permissions,omitempty"`
	Validation  map[string]any   `json:"validation,omitempty"`
	Presets     map[string]any   `json:"presets,omitempty"`
}

// ListPermissions returns all permissions.
func (c *Client) ListPermissions(ctx context.Context) ([]Permission, error) {
	raw, err := c.Get(ctx, "permissions", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: list permissions: %w", err)
	}

	var perms []Permission
	if err := json.Unmarshal(raw, &perms); err != nil {
		return nil, fmt.Errorf("directus: unmarshal permissions: %w", err)
	}

	return perms, nil
}

// CreatePermission creates a permission rule.
func (c *Client) CreatePermission(ctx context.Context, perm Permission) (*Permission, error) {
	raw, err := c.Post(ctx, "permissions", perm)
	if err != nil {
		return nil, fmt.Errorf("directus: create permission: %w", err)
	}

	var created Permission
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, fmt.Errorf("directus: unmarshal created permission: %w", err)
	}

	return &created, nil
}

// UpdatePermission updates a permission rule.
func (c *Client) UpdatePermission(ctx context.Context, id int, perm Permission) (*Permission, error) {
	raw, err := c.Patch(ctx, fmt.Sprintf("permissions/%d", id), perm)
	if err != nil {
		return nil, fmt.Errorf("directus: update permission %d: %w", id, err)
	}

	var updated Permission
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated permission: %w", err)
	}

	return &updated, nil
}

// DeletePermission removes a permission rule.
func (c *Client) DeletePermission(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("permissions/%d", id))
}

// GrantFullAccess creates CRUD permissions for a collection on a policy.
// This grants create, read, update, and delete access with no field restrictions.
func (c *Client) GrantFullAccess(ctx context.Context, policyID, collection string) error {
	for _, action := range []PermissionAction{ActionCreate, ActionRead, ActionUpdate, ActionDelete} {
		_, err := c.CreatePermission(ctx, Permission{
			Collection: collection,
			Action:     action,
			Policy:     policyID,
			Fields:     []string{"*"},
		})
		if err != nil {
			return fmt.Errorf("directus: grant %s on %s: %w", action, collection, err)
		}
	}

	return nil
}

// User represents a Directus user.
type User struct {
	ID        string  `json:"id,omitempty"`
	Email     string  `json:"email,omitempty"`
	FirstName string  `json:"first_name,omitempty"`
	LastName  string  `json:"last_name,omitempty"`
	Status    string  `json:"status,omitempty"`
	Role      string  `json:"role,omitempty"`
	Token     *string `json:"token,omitempty"`
}

// GetCurrentUser returns the authenticated user.
func (c *Client) GetCurrentUser(ctx context.Context) (*User, error) {
	raw, err := c.Get(ctx, "users/me", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: get current user: %w", err)
	}

	var user User
	if err := json.Unmarshal(raw, &user); err != nil {
		return nil, fmt.Errorf("directus: unmarshal user: %w", err)
	}

	return &user, nil
}

// UpdateUser updates a user by ID.
func (c *Client) UpdateUser(ctx context.Context, id string, user User) (*User, error) {
	raw, err := c.Patch(ctx, "users/"+id, user)
	if err != nil {
		return nil, fmt.Errorf("directus: update user %s: %w", id, err)
	}

	var updated User
	if err := json.Unmarshal(raw, &updated); err != nil {
		return nil, fmt.Errorf("directus: unmarshal updated user: %w", err)
	}

	return &updated, nil
}

// ListUsers returns all users.
func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	raw, err := c.Get(ctx, "users", nil)
	if err != nil {
		return nil, fmt.Errorf("directus: list users: %w", err)
	}

	var users []User
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, fmt.Errorf("directus: unmarshal users: %w", err)
	}

	return users, nil
}
