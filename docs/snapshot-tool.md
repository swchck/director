# Snapshot Tool (`cmd/snapshot/`)

CLI tool for capturing and restoring Directus schema and data between instances. Useful for environment migration, seeding development databases, and replicating production setups.

## Usage

```bash
snapshot <pull|apply> [flags]
```

## Pull Command

Reads schema and/or data from a Directus instance and saves to a local directory.

```bash
# Pull schema only
go run ./cmd/snapshot pull --url=https://remote.example.com --token=xxx --output=snapshot/

# Pull schema + data for specific collections
go run ./cmd/snapshot pull --url=https://remote.example.com --token=xxx --output=snapshot/ --data=products,settings

# Pull schema + data for ALL collections
go run ./cmd/snapshot pull --url=https://remote.example.com --token=xxx --output=snapshot/ --data='*'
```

### Pull Flags

| Flag | Default | Description |
|---|---|---|
| `--url` | (required) | Directus base URL |
| `--token` | (required) | Directus access token |
| `--output` | `snapshot` | Output directory |
| `--data` | (none) | Comma-separated collection names, or `*` for all |

### What Pull Saves

| File | Content |
|---|---|
| `meta.json` | Directus version, source URL, timestamp |
| `schema.json` | Full schema snapshot (collections, fields, relations) |
| `flows.json` | All flows with their operations |
| `data/{collection}.json` | Item data per collection (with pagination) |
| `data/_manifest.json` | Map of collection name to item count |

Pull fetches data with pagination (500 items per page) and runs up to 6 concurrent collection pulls.

## Apply Command

Writes schema and/or data to a Directus instance.

```bash
# Apply schema only
go run ./cmd/snapshot apply --url=http://localhost:8055 --token=xxx --input=snapshot/

# Apply schema + data
go run ./cmd/snapshot apply --url=http://localhost:8055 --token=xxx --input=snapshot/ --data

# Apply with interactive TUI
go run ./cmd/snapshot apply --url=http://localhost:8055 --token=xxx --input=snapshot/ --data --tui
```

### Apply Flags

| Flag | Default | Description |
|---|---|---|
| `--url` | `http://localhost:8055` | Target Directus base URL |
| `--token` | `e2e-test-token` | Directus access token or JWT |
| `--input` | `snapshot` | Input directory |
| `--data` | `false` | Also apply item data |
| `--email` | `admin@example.com` | Admin email for token refresh |
| `--password` | `admin` | Admin password for token refresh |
| `--tui` | `false` | Interactive TUI with progress bars |
| `--force` | `true` | Force schema diff across Directus versions |

### Schema Application

The apply command uses a two-strategy approach:

1. **Native schema diff** (preferred): Uses Directus `/schema/diff` + `/schema/apply` endpoints. Fastest and most accurate when source and target run the same Directus version.

2. **Collections API fallback**: If the native diff fails or returns errors, falls back to creating collections, fields, and relations individually via the REST API. This handles cross-version migrations where the native diff may not work.

**Collections API fallback strategy:**
- Pass 1: Create folder collections (no DB table) -- these are parents for groups
- Pass 2: Create table collections with PK fields only
- Pass 3: Set group references via PATCH (all collections exist by this point)
- Pass 4: Create non-PK fields
- Pass 5: Create relations

### Flow Application

Flows are applied with full operation chain reconstruction:
1. Create the flow (without operations)
2. Create each operation individually, mapping old IDs to new IDs
3. Link resolve/reject chains using the old-to-new ID mapping
4. Link the flow to its first operation

### Multi-Pass Data Insertion

Data is inserted using a multi-pass strategy to handle foreign key dependencies:

```
Pass 1: Insert all items for all collections
         - Some may fail due to FK constraints (related items not yet inserted)
Pass 2: Retry failed items (FK deps may now be satisfied)
Pass 3-5: Continue retrying until all items succeed or max passes reached
```

- Up to 5 passes maximum
- 4 concurrent collection workers
- Singletons are applied via PATCH (not POST)
- Virtual alias fields are stripped before insertion
- Token refresh happens automatically during long operations

### Version Matching

For best schema compatibility, match the Directus version between source and target:

```bash
# Read version from snapshot metadata and start matching Directus
DIRECTUS_VERSION=$(cat snapshot/meta.json | jq -r .directus_version)
docker compose up -d
```

## TUI Features

When `--tui` is passed, the apply command displays an interactive terminal UI built with Bubbletea and Lipgloss.

### Progress View (during apply)

- Animated spinner with current phase name and elapsed time
- Schema stats: collections created/skipped, fields created/skipped, relations created
- Flow and singleton progress counters
- Data progress bar with percentage, rate (items/s), and ETA
- Per-collection status: pending, active (with mini progress bar), done, or error
- Auto-scrolling collection list showing recent completions, active work, and pending items
- Log messages during schema/flows phase

### Summary View (after completion)

- Overall status (Complete/Partial) with elapsed time
- Results grid: collections, fields, relations, flows, singletons, data totals
- Tabbed navigation: All, Errors, Warnings, Logs
- Scrollable list with keyboard navigation (up/down/j/k, pgup/pgdn, home/end)
- Detail pane for selected item (collection stats, error messages)
- Tab/arrow keys to switch between filter views

### Keyboard Controls

| Key | Action |
|---|---|
| `q`, `Ctrl+C` | Quit |
| `up`/`k`, `down`/`j` | Navigate list |
| `pgup`, `pgdn` | Scroll page |
| `home`, `end` | Jump to start/end |
| `tab`/`right`/`l` | Next tab |
| `shift+tab`/`left`/`h` | Previous tab |

## Taskfile Commands

The project `Taskfile.yml` provides convenience commands for the snapshot tool:

| Task | Description |
|---|---|
| `task snapshot:pull` | Pull schema + data from remote (configure via `REMOTE_URL`, `REMOTE_TOKEN`) |
| `task snapshot:apply` | Apply snapshot to local Directus (JWT login, schema + data) |
| `task snapshot:apply:tui` | Apply snapshot with interactive TUI progress |
| `task snapshot:apply:matched` | Start Directus matching snapshot version, then apply |
| `task snapshot:apply:matched:tui` | Version-matched apply with TUI |

The apply tasks automatically obtain a JWT via login (required for Directus 11 admin operations where static tokens do not inherit runtime policy changes).
