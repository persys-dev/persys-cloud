package models

import "time"

type Project struct {
	ID            uint   `gorm:"primaryKey"`
	Name          string `gorm:"type:varchar(191);uniqueIndex;not null"`
	RepoURL       string `gorm:"type:text;not null"`
	DefaultBranch string `gorm:"type:varchar(191);default:'main'"`
	ClusterID     string `gorm:"type:varchar(191);index"`
	BuildType     string `gorm:"type:varchar(64);default:'dockerfile'"`
	BuildMode     string `gorm:"type:varchar(64);default:'standalone'"`
	Strategy      string `gorm:"type:varchar(64);default:'local'"`
	NexusRepo     string `gorm:"type:text"`
	PipelineYAML  string `gorm:"type:text"`
	AutoDeploy    bool   `gorm:"default:false"`
	ImageName     string `gorm:"type:varchar(191)"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type GitHubCredential struct {
	ID         uint   `gorm:"primaryKey"`
	UserID     string `gorm:"type:varchar(191);index;not null"`
	UserLogin  string `gorm:"type:varchar(191);index"`
	ScopeCSV   string `gorm:"type:text"`
	SecretPath string `gorm:"type:text;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
