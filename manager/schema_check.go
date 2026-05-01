package manager

import (
	"context"

	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
)

// schemaCheckEntry is captured at registration time so the manager can
// later compare a Go struct against the live Directus schema. Generic-source
// registrations (RegisterCollectionSource / RegisterSingletonSource) do not
// produce entries — schema check only applies to Directus-backed configs.
type schemaCheckEntry struct {
	collection string
	client     *directus.Client
	sample     any
}

// runSchemaChecks iterates captured Directus-backed registrations, fetches
// their live schemas, and logs warnings for any Go-declared field that is
// absent in Directus. Off-by-default (gated by WithSchemaCheck); intended as
// a startup smoke test for catching renamed/deleted fields before they cause
// silent data loss.
//
// Failures fetching a schema are logged and skipped — schema check should
// never block startup.
func (m *Manager) runSchemaChecks(ctx context.Context) {
	if !m.schemaCheck {
		return
	}

	for _, entry := range m.schemaCheckEntries {
		fields, err := entry.client.ListFields(ctx, entry.collection)
		if err != nil {
			m.logger.Warn("manager: schema check skipped — could not list fields",
				dlog.String("collection", entry.collection),
				dlog.Err(err),
			)
			continue
		}

		drifts := directus.CompareStruct(fields, entry.sample)
		if len(drifts) == 0 {
			m.logger.Debug("manager: schema check passed",
				dlog.String("collection", entry.collection),
				dlog.Int("directus_fields", len(fields)),
			)
			continue
		}

		for _, d := range drifts {
			m.logger.Warn("manager: schema drift detected",
				dlog.String("collection", entry.collection),
				dlog.String("go_field", d.Field),
				dlog.String("json_tag", d.JSONTag),
				dlog.String("reason", d.Reason),
			)
		}
	}
}
