package config

import "time"

type Runtime struct {
	Root              string
	Interval          time.Duration
	CPUSampleDuration time.Duration
	LogLevel          string
}

func Default() Runtime {
	return Runtime{
		Root:              "/",
		Interval:          time.Second,
		CPUSampleDuration: 200 * time.Millisecond,
		LogLevel:          "warn",
	}
}
