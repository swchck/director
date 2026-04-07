# Package: `directus/`

Pure Go HTTP client for the Directus REST API. No external Directus libraries.

## Components

### Client (`client.go`)

Low-level HTTP client that handles:
- Authentication via Bearer token (injected as `http.RoundTripper` in `auth.go`)
- Automatic unwrapping of Directus `{"data": ...}` response envelope
- Error mapping: HTTP status codes -> sentinel errors (`ErrNotFound`, `ErrUnauthorized`, etc.)

```go
client := directus.NewClient("https://directus.example.com", "my-token",
    directus.WithLogger(logger),
    directus.WithHTTPClient(customHTTPClient), // for retries, rate limiting, etc.
)
```

The auth transport wraps the underlying `http.Client.Transport`, so middleware like retries can be injected by the consumer via a custom `http.Client`.

### Items[T] (`items.go`)

Generic typed wrapper for multi-item collections. Methods:

| Method | HTTP | Path |
|---|---|---|
| `List(ctx, ...QueryOption)` | GET | `/items/{collection}` |
| `Get(ctx, id, ...QueryOption)` | GET | `/items/{collection}/{id}` |
| `Create(ctx, item)` | POST | `/items/{collection}` |
| `Update(ctx, id, item)` | PATCH | `/items/{collection}/{id}` |
| `Delete(ctx, id)` | DELETE | `/items/{collection}/{id}` |
| `MaxDateUpdated(ctx)` | GET | `/items/{collection}?sort=-date_updated&limit=1&fields=date_updated` |

`MaxDateUpdated` is the lightweight version-check call -- fetches a single field from one row.

### Singleton[T] (`singleton.go`)

For Directus singleton collections (one object, no array):

| Method | HTTP | Path |
|---|---|---|
| `Get(ctx, ...QueryOption)` | GET | `/items/{collection}` |
| `Update(ctx, item)` | PATCH | `/items/{collection}` |
| `DateUpdated(ctx)` | GET | `/items/{collection}?fields=date_updated` |

### Filter & Query Builder (`filter.go`)

Builds Directus query parameters as JSON:

```go
// Simple field filter
directus.Field("status", "_eq", "published")

// Logical operators
directus.And(
    directus.Field("status", "_eq", "published"),
    directus.Field("level", "_gte", 5),
)

// Relational deep queries (M2O, O2M, M2M, M2A)
directus.WithDeep("tags", directus.RelationQuery{
    Filter: directus.Field("status", "_eq", "active"),
    Sort:   []string{"-priority"},
    Limit:  new(int) // or &myVar for non-zero,
})

// Translation convenience
directus.WithTranslations("languages_code", "en-US")
// Equivalent to:
//   WithFields("*", "translations.*")
//   WithDeep("translations", RelationQuery{Filter: Field("languages_code", "_eq", "en-US")})
```

### WebSocket (`websocket.go`)

Optional real-time change detection:

```go
ws := directus.NewWSClient("https://directus.example.com", "token")
events, _ := ws.Subscribe(ctx, "businesses", "products")

for event := range events {
    // event.Collection, event.Action ("create"/"update"/"delete"), event.Keys
}
```

## Error Handling

All Directus API errors are wrapped in `*ResponseError` which implements `Unwrap()`:

```go
_, err := items.Get(ctx, "999")
if errors.Is(err, directus.ErrNotFound) {
    // handle 404
}

var re *directus.ResponseError
if errors.As(err, &re) {
    // re.StatusCode, re.Errors[0].Message
}
```

## Schema Management (`schema.go`)

Create collections, fields, and relations programmatically:

```go
dc.CreateCollection(ctx, directus.CreateCollectionInput{
    Collection: "products",
    Fields: []directus.FieldInput{
        directus.PrimaryKeyField("id"),
        directus.DateUpdatedField(),
        directus.StringField("name"),
        directus.M2OField("category_id", "categories"),
    },
})
```

### Collection Folders

Organize collections in the Directus sidebar:

```go
// Create a folder.
dc.CreateCollectionFolder(ctx, "content", &directus.CollectionMeta{
    Icon: "folder", Collapse: directus.CollapseOpen,
})

// Create a collection inside the folder.
dc.CreateCollection(ctx, directus.CreateCollectionInput{
    Collection: "articles",
    Meta:       &directus.CollectionMeta{Group: "content"},
    Fields:     []directus.FieldInput{directus.PrimaryKeyField("id")},
})

// Move an existing collection into a folder.
dc.MoveCollectionToFolder(ctx, "pages", "content")

// Delete a collection.
dc.DeleteCollection(ctx, "pages")
```

Collapse modes: `CollapseOpen`, `CollapseClosed`, `CollapseLocked`.

### Special Field Handling

Directus 11 quirk: fields with `special` metadata (date-created, date-updated, uuid, hash, conceal) must be created separately after the collection. `CreateCollection` handles this automatically via `splitSpecialFields`.

### Field Presets

| Helper | Type |
|---|---|
| `PrimaryKeyField(name)` | Auto-increment integer PK |
| `UUIDPrimaryKeyField(name)` | UUID PK |
| `StatusField()` | Draft/published/archived dropdown |
| `SortField()` | Integer sort field |
| `DateCreatedField()` | Timestamp with `date-created` special |
| `DateUpdatedField()` | Timestamp with `date-updated` special |
| `StringField(name)` | Text input |
| `TextField(name)` | Multiline text |
| `IntegerField(name)` | Integer input |
| `FloatField(name)` | Float input |
| `DecimalField(name)` | Decimal input |
| `BooleanField(name)` | Toggle switch |
| `JSONField(name)` | JSON code editor |
| `M2OField(name, related)` | Many-to-One dropdown |

### Relations

**Builder helpers:**

| Helper | Creates |
|---|---|
| `M2O(collection, field, related)` | Foreign key on the "many" side |
| `O2M(collection, alias, related, fk)` | Virtual alias on the "one" side |
| `M2M(M2MInput)` | Both sides of a junction-based M2M |
| `Translations(collection, junction, sourceFK, langFK, langCollection)` | Translation M2M relations |

**Relation CRUD:**

| Method | HTTP | Path |
|---|---|---|
| `CreateRelation(ctx, input)` | POST | `/relations` |
| `DeleteRelation(ctx, collection, field)` | DELETE | `/relations/{collection}/{field}` |
| `GetRelations(ctx, collection)` | GET | `/relations` or `/relations/{collection}` |

### File Folders (`folders.go`)

Organize uploaded files/assets:

```go
folder, _ := dc.CreateFolder(ctx, directus.Folder{Name: "Photos"})
dc.CreateFolder(ctx, directus.Folder{Name: "Vacation", Parent: &folder.ID})
```

## ACL Management (`acl.go`)

Manage Directus roles, policies, permissions, and users.

### Roles

```go
role, _ := dc.CreateRole(ctx, directus.Role{Name: "Editor"})
roles, _ := dc.ListRoles(ctx)
role, _ := dc.GetRole(ctx, id)
dc.UpdateRole(ctx, id, directus.Role{Name: "Senior Editor"})
dc.DeleteRole(ctx, id)
```

### Policies

```go
policy, _ := dc.CreatePolicy(ctx, directus.Policy{
    Name:      "Editor Policy",
    AdminAccess: false,
    AppAccess:   true,
})
policies, _ := dc.ListPolicies(ctx)
dc.UpdatePolicy(ctx, id, directus.Policy{Name: "Updated"})
dc.DeletePolicy(ctx, id)
```

### Permissions

```go
// Create individual permission.
perm, _ := dc.CreatePermission(ctx, directus.Permission{
    Collection: "products",
    Action:     directus.ActionRead,
    Policy:     policyID,
    Fields:     []string{"*"},
})

// Grant full CRUD on a collection for a policy.
dc.GrantFullAccess(ctx, policyID, "products")

// List all permissions.
perms, _ := dc.ListPermissions(ctx)
dc.UpdatePermission(ctx, id, perm)
dc.DeletePermission(ctx, id)
```

Permission actions: `ActionCreate`, `ActionRead`, `ActionUpdate`, `ActionDelete`.

### Users

```go
me, _ := dc.GetCurrentUser(ctx)
users, _ := dc.ListUsers(ctx)
dc.UpdateUser(ctx, id, directus.User{FirstName: "Alice"})
```

### Admin Access

```go
// Grant admin access to the Administrator policy (Directus 11 workaround).
dc.GrantAdminAccess(ctx)
```

## Flows & Operations (`flows.go`)

Define Directus automation flows as code.

### Creating Flows

```go
// Hook flow triggered on item creation.
flow, _ := dc.CreateFlow(ctx, directus.NewHookFlow("Notify on Create",
    directus.HookFlowOptions{
        Type:        "action",
        Scope:       []string{"items.create"},
        Collections: []string{"orders"},
    },
))

// Webhook flow.
flow, _ := dc.CreateFlow(ctx, directus.NewWebhookFlow("External Trigger",
    directus.WebhookFlowOptions{Method: "POST"},
))

// Schedule flow.
flow, _ := dc.CreateFlow(ctx, directus.NewScheduleFlow("Hourly Cleanup",
    directus.ScheduleFlowOptions{Cron: "0 * * * *"},
))

// Manual flow.
flow, _ := dc.CreateFlow(ctx, directus.NewManualFlow("Run Report"))
```

**Trigger types:** `hook`, `webhook`, `schedule`, `manual`, `operation`

**Flow accountability:** `$public`, `$trigger`, `$full`

### Creating Operations

```go
logOp, _ := dc.CreateOperation(ctx, directus.Operation{
    Name: "Log", Key: "log", Type: directus.OpLog,
    Flow: flow.ID, Options: map[string]any{"message": "Order created"},
})

// Link the flow to its first operation.
dc.UpdateFlow(ctx, flow.ID, directus.Flow{Operation: &logOp.ID})
```

**Operation builder helpers:**

| Helper | Type |
|---|---|
| `NewLogOperation(key, message)` | Log message |
| `NewRequestOperation(key, method, url)` | HTTP request |
| `NewCreateItemOperation(key, collection, payload)` | Insert item |
| `NewConditionOperation(key, filter)` | Conditional branch |

**Operation chaining:** Each operation has `Resolve` (next on success) and `Reject` (next on failure) pointers to other operation UUIDs.

**Operation types:** `log`, `mail`, `notification`, `item-create`, `item-read`, `item-update`, `item-delete`, `request`, `sleep`, `transform`, `trigger`, `condition`, `exec`

### Parsing Operations from Flows

```go
flow, _ := dc.GetFlow(ctx, id, directus.WithFields("*", "operations.*"))
ops, _ := flow.ParseOperations() // handles both full objects and UUID arrays
```

### Flow CRUD

| Method | HTTP | Path |
|---|---|---|
| `ListFlows(ctx, opts...)` | GET | `/flows` |
| `GetFlow(ctx, id, opts...)` | GET | `/flows/{id}` |
| `CreateFlow(ctx, flow)` | POST | `/flows` |
| `UpdateFlow(ctx, id, flow)` | PATCH | `/flows/{id}` |
| `DeleteFlow(ctx, id)` | DELETE | `/flows/{id}` |
| `TriggerWebhookFlow(ctx, id, payload)` | POST | `/flows/trigger/{id}` |

### Operation CRUD

| Method | HTTP | Path |
|---|---|---|
| `ListOperations(ctx, opts...)` | GET | `/operations` |
| `GetOperation(ctx, id)` | GET | `/operations/{id}` |
| `CreateOperation(ctx, op)` | POST | `/operations` |
| `UpdateOperation(ctx, id, op)` | PATCH | `/operations/{id}` |
| `DeleteOperation(ctx, id)` | DELETE | `/operations/{id}` |

## Authentication (`authentication.go`)

JWT-based authentication for operations that require dynamic tokens (e.g., Directus 11 admin operations where static tokens do not inherit runtime policy changes).

```go
// Login with email/password.
auth, _ := dc.Login(ctx, "admin@example.com", "password")
// auth.AccessToken, auth.RefreshToken, auth.Expires

// Refresh an access token.
auth, _ = dc.RefreshToken(ctx, auth.RefreshToken)

// Logout (invalidate refresh token).
dc.Logout(ctx, auth.RefreshToken)

// Password reset flow.
dc.RequestPasswordReset(ctx, "user@example.com")
dc.ResetPassword(ctx, resetToken, "newpassword")
```

## Additional API Endpoints

The following endpoints provide typed access to Directus system resources. All use generic CRUD helpers (`list`, `get`, `create`, `update`) that handle JSON marshaling and the Directus response envelope.

### Activity (`activity.go`)

Read-only access to the Directus activity log.

| Method | HTTP | Path |
|---|---|---|
| `ListActivity(ctx, opts...)` | GET | `/activity` |
| `GetActivity(ctx, id)` | GET | `/activity/{id}` |

### Comments (`comments.go`)

Item-level comments (annotations on collection items).

| Method | HTTP | Path |
|---|---|---|
| `ListComments(ctx, opts...)` | GET | `/comments` |
| `GetComment(ctx, id)` | GET | `/comments/{id}` |
| `CreateComment(ctx, comment)` | POST | `/comments` |
| `UpdateComment(ctx, id, comment)` | PATCH | `/comments/{id}` |
| `DeleteComment(ctx, id)` | DELETE | `/comments/{id}` |

### Dashboards & Panels (`dashboards.go`)

Directus Insights dashboards and their panels.

| Method | HTTP | Path |
|---|---|---|
| `ListDashboards(ctx, opts...)` | GET | `/dashboards` |
| `GetDashboard(ctx, id)` | GET | `/dashboards/{id}` |
| `CreateDashboard(ctx, d)` | POST | `/dashboards` |
| `UpdateDashboard(ctx, id, d)` | PATCH | `/dashboards/{id}` |
| `DeleteDashboard(ctx, id)` | DELETE | `/dashboards/{id}` |
| `ListPanels(ctx, opts...)` | GET | `/panels` |
| `GetPanel(ctx, id)` | GET | `/panels/{id}` |
| `CreatePanel(ctx, p)` | POST | `/panels` |
| `UpdatePanel(ctx, id, p)` | PATCH | `/panels/{id}` |
| `DeletePanel(ctx, id)` | DELETE | `/panels/{id}` |

### Extensions (`extensions.go`)

List and enable/disable installed extensions.

| Method | HTTP | Path |
|---|---|---|
| `ListExtensions(ctx)` | GET | `/extensions` |
| `UpdateExtension(ctx, name, ext)` | PATCH | `/extensions/{name}` |
| `Metrics(ctx)` | GET | `/server/metrics` |

### Files (`files.go`)

File/asset management including metadata updates, deletion, URL import, and asset URL generation.

| Method | HTTP | Path |
|---|---|---|
| `ListFiles(ctx, opts...)` | GET | `/files` |
| `GetFile(ctx, id)` | GET | `/files/{id}` |
| `UpdateFile(ctx, id, file)` | PATCH | `/files/{id}` |
| `DeleteFile(ctx, id)` | DELETE | `/files/{id}` |
| `ImportFile(ctx, input)` | POST | `/files/import` |
| `AssetURL(id, key)` | -- | Returns URL string: `/assets/{id}?key={key}` |

### Notifications (`notifications.go`)

In-app notification management.

| Method | HTTP | Path |
|---|---|---|
| `ListNotifications(ctx, opts...)` | GET | `/notifications` |
| `GetNotification(ctx, id)` | GET | `/notifications/{id}` |
| `CreateNotification(ctx, n)` | POST | `/notifications` |
| `UpdateNotification(ctx, id, n)` | PATCH | `/notifications/{id}` |
| `DeleteNotification(ctx, id)` | DELETE | `/notifications/{id}` |

### Presets (`presets.go`)

Layout/filter presets (bookmarks) for the Directus Data Studio.

| Method | HTTP | Path |
|---|---|---|
| `ListPresets(ctx, opts...)` | GET | `/presets` |
| `GetPreset(ctx, id)` | GET | `/presets/{id}` |
| `CreatePreset(ctx, p)` | POST | `/presets` |
| `UpdatePreset(ctx, id, p)` | PATCH | `/presets/{id}` |
| `DeletePreset(ctx, id)` | DELETE | `/presets/{id}` |

### Revisions (`revisions.go`)

Content revision history (read-only).

| Method | HTTP | Path |
|---|---|---|
| `ListRevisions(ctx, opts...)` | GET | `/revisions` |
| `GetRevision(ctx, id)` | GET | `/revisions/{id}` |

### Server (`server.go`)

Server health, info, specs, settings, and utilities.

| Method | HTTP | Path |
|---|---|---|
| `ServerHealth(ctx)` | GET | `/server/health` |
| `ServerInfo(ctx)` | GET | `/server/info` |
| `ServerPing(ctx)` | GET | `/server/ping` |
| `ServerSpecsOAS(ctx)` | GET | `/server/specs/oas` |
| `ServerSpecsGraphQL(ctx)` | GET | `/server/specs/graphql` |
| `GetSettings(ctx)` | GET | `/settings` |
| `UpdateSettings(ctx, s)` | PATCH | `/settings` |
| `SchemaSnapshot(ctx)` | GET | `/schema/snapshot` |
| `SchemaDiff(ctx, snapshot, force)` | POST | `/schema/diff` |
| `SchemaApply(ctx, diff)` | POST | `/schema/apply` |

**Utilities:**

| Method | HTTP | Path |
|---|---|---|
| `HashGenerate(ctx, value)` | POST | `/utils/hash/generate` |
| `HashVerify(ctx, value, hash)` | POST | `/utils/hash/verify` |
| `RandomString(ctx, length)` | GET | `/utils/random/string` |
| `ClearCache(ctx)` | POST | `/utils/cache/clear` |
| `SortItems(ctx, collection, item, to)` | POST | `/utils/sort/{collection}` |

### Shares (`shares.go`)

Public shared links for items.

| Method | HTTP | Path |
|---|---|---|
| `ListShares(ctx, opts...)` | GET | `/shares` |
| `GetShare(ctx, id)` | GET | `/shares/{id}` |
| `CreateShare(ctx, s)` | POST | `/shares` |
| `UpdateShare(ctx, id, s)` | PATCH | `/shares/{id}` |
| `DeleteShare(ctx, id)` | DELETE | `/shares/{id}` |
| `ShareInfo(ctx, id)` | GET | `/shares/info/{id}` |

### Translations (`translations.go`)

Custom UI translation strings (Directus admin interface translations, not content translations).

| Method | HTTP | Path |
|---|---|---|
| `ListTranslations(ctx, opts...)` | GET | `/translations` |
| `GetTranslation(ctx, id)` | GET | `/translations/{id}` |
| `CreateTranslation(ctx, t)` | POST | `/translations` |
| `UpdateTranslation(ctx, id, t)` | PATCH | `/translations/{id}` |
| `DeleteTranslation(ctx, id)` | DELETE | `/translations/{id}` |

### Content Versions (`versions.go`)

Draft/staging content versions for items.

| Method | HTTP | Path |
|---|---|---|
| `ListContentVersions(ctx, opts...)` | GET | `/versions` |
| `GetContentVersion(ctx, id)` | GET | `/versions/{id}` |
| `CreateContentVersion(ctx, v)` | POST | `/versions` |
| `UpdateContentVersion(ctx, id, v)` | PATCH | `/versions/{id}` |
| `DeleteContentVersion(ctx, id)` | DELETE | `/versions/{id}` |
| `CompareContentVersion(ctx, id)` | GET | `/versions/{id}/compare` |
| `PromoteContentVersion(ctx, id)` | POST | `/versions/{id}/promote` |
| `SaveContentVersion(ctx, id, data)` | POST | `/versions/{id}/save` |
