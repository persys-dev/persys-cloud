package features

import (
	"context"

	"github.com/persys-dev/persys-cloud/persys-intelligence/internal/model"
)

type Extractor interface {
	Extract(ctx context.Context) ([]model.FeatureSnapshot, error)
}

type StaticExtractor struct {
	defaultWorkload string
}

func NewStaticExtractor(defaultWorkload string) *StaticExtractor {
	return &StaticExtractor{defaultWorkload: defaultWorkload}
}

func (e *StaticExtractor) Extract(_ context.Context) ([]model.FeatureSnapshot, error) {
	return []model.FeatureSnapshot{
		{
			Workload:        e.defaultWorkload,
			CPU5mAvg:        0,
			CPU1hTrend:      "stable",
			ErrorRateDelta:  "0%",
			RecentDeploy:    false,
			RetryCount:      0,
			NodePressure:    "normal",
			GeneratedSource: "static-extractor",
		},
	}, nil
}
