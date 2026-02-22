package models

type Job struct {
	ID         uint   `gorm:"primaryKey"`
	ProjectID  uint   `gorm:"index"`
	Type       string `gorm:"type:varchar(64)"` // build, deploy, etc.
	Status     string `gorm:"type:varchar(64)"` // pending, running, success, failed
	CommitHash string `gorm:"type:varchar(191)"`
	Log        string `gorm:"type:text"`
	CreatedAt  int64
	UpdatedAt  int64
}
