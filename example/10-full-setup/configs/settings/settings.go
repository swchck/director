// Package settings is a self-contained config unit for the "app_settings" singleton.
package settings

import (
	"github.com/swchck/director/config"
	"github.com/swchck/director/directus"
	"github.com/swchck/director/manager"
	"github.com/swchck/director/source"
)

// AppSettings is the Directus singleton type.
type AppSettings struct {
	MaxPlayers      int     `json:"max_players"`
	TickRate        float64 `json:"tick_rate"`
	MaintenanceMode bool    `json:"maintenance_mode"`
}

// Settings holds the singleton and exposes it as read-only.
type Settings struct {
	s   *config.Singleton[AppSettings]
	All config.ReadableSingleton[AppSettings]
	src source.SingletonSource[AppSettings]
}

// Config creates a Settings config unit sourced from Directus.
func Config(dc *directus.Client) *Settings {
	s := config.NewSingleton[AppSettings]("app_settings")
	return &Settings{
		s:   s,
		All: s,
		src: source.FromDirectusSingleton(directus.NewSingleton[AppSettings](dc, "app_settings")),
	}
}

// Register registers the data source with the manager.
func (s *Settings) Register(m *manager.Manager) {
	manager.RegisterSingletonSource(m, s.s, s.src)
}

// OnChange registers a callback that fires when settings update.
func (s *Settings) OnChange(fn func(old, new *AppSettings)) {
	s.s.OnChange(fn)
}
