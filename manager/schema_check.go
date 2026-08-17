package manager

import (
	"context"

	"github.com/swchck/director/directus"
	dlog "github.com/swchck/director/log"
)

// schemaCheckEntry is captured at registration time so the manager can compare a Go
// struct against the live Directus schema. Only Directus registrations make one.
type schemaCheckEntry struct {
	collection string
	client     *directus.Client
	sample     any
}

// runSchemaChecks warns about Go-declared fields the live Directus schema lacks. See
// WithSchemaCheck; an unfetchable schema is skipped, never fatal.
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
