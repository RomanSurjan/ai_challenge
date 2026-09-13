package main

type RuntimeSettings struct {
	Strategy       string `json:"strategy"`
	WindowMessages int    `json:"window_messages"`
}

func runtimeSettingsFromConfig(cfg AgentConfig) RuntimeSettings {
	return RuntimeSettings{
		Strategy:       normalizeStrategyName(cfg.Strategy),
		WindowMessages: cfg.Window,
	}
}

func validateRuntimeSettings(settings RuntimeSettings) error {
	if _, err := NewContextStrategy(settings.Strategy, settings.WindowMessages); err != nil {
		return err
	}
	return nil
}
