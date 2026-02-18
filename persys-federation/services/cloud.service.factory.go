package services

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
)

type cloudServiceImpl struct{}

// NewCloudService returns a no-op CloudService implementation.
// This keeps the service wiring compile-safe while CloudService methods are still being defined.
func NewCloudService(_ *mongo.Database, _ context.Context, _ ...any) CloudService {
	return &cloudServiceImpl{}
}
