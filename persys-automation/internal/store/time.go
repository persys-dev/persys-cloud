package store

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func toProtoTime(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t.UTC())
}
