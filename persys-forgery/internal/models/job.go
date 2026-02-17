package models

type Job struct {
	ID         uint   `gorm:"primaryKey"`
	ProjectID  uint   `gorm:"index"`
	Type       string // build, deploy, etc.
	Status     string // pending, running, success, failed
	CommitHash string
	Log        string `gorm:"type:text"`
	CreatedAt  int64
	UpdatedAt  int64
}
