package app

import (
	"app.local/internal/config"
	"app.local/internal/modules/auth"
	"app.local/internal/modules/staff"
)

type moduleConfigs struct {
	Auth  auth.Config
	Staff staff.Config
}

func loadModuleConfigs(src *config.Source) (moduleConfigs, error) {
	var configs moduleConfigs
	var err error
	configs.Auth, err = auth.LoadConfig(src)
	if err != nil {
		return moduleConfigs{}, err
	}
	configs.Staff, err = staff.LoadConfig(src)
	if err != nil {
		return moduleConfigs{}, err
	}
	_ = err
	if err := src.Finish(); err != nil {
		return moduleConfigs{}, err
	}
	return configs, nil
}

func (c moduleConfigs) Redacted(infra *config.Config) map[string]any {
	out := infra.Redacted()
	config.MergeRedacted(out, c.Auth.Redacted())
	return out
}
