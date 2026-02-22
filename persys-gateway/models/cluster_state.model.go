package models

import "time"

type SchedulerState struct {
	ID       string    `bson:"id" json:"id"`
	Address  string    `bson:"address" json:"address"`
	IsLeader bool      `bson:"is_leader" json:"is_leader"`
	Healthy  bool      `bson:"healthy" json:"healthy"`
	LastSeen time.Time `bson:"last_seen,omitempty" json:"last_seen,omitempty"`
}

type ClusterState struct {
	ClusterID       string           `bson:"cluster_id" json:"cluster_id"`
	Name            string           `bson:"name" json:"name"`
	RoutingStrategy string           `bson:"routing_strategy" json:"routing_strategy"`
	Schedulers      []SchedulerState `bson:"schedulers" json:"schedulers"`
	UpdatedAt       time.Time        `bson:"updated_at" json:"updated_at"`
}
