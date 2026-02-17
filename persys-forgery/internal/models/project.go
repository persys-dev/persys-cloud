package models

import "time"

type Project struct {
	ID            uint   `gorm:"primaryKey"`
	Name          string `gorm:"uniqueIndex;not null"`
	RepoURL       string `gorm:"not null"`
	DefaultBranch string `gorm:"default:'main'"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
