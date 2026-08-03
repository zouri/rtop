package collector

import (
	"context"

	"rtop/internal/model"
)

type Capability struct {
	Name   string             `json:"name"`
	Status model.MetricStatus `json:"status"`
	Source string             `json:"source,omitempty"`
	Reason string             `json:"reason,omitempty"`
}

type Collector interface {
	Name() string
	Probe(ctx context.Context) Capability
	Collect(ctx context.Context) ([]model.Metric, error)
}
