package secrets

import (
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"gorm.io/gorm"
)

type Manager struct {
	DB *gorm.DB
}

func (m *Manager) Set(project, key, value string) error {
	return SetProjectSecret(m.DB, project, key, value)
}

func (m *Manager) Get(project, key string) (string, error) {
	return GetProjectSecret(m.DB, project, key)
}

func (m *Manager) List(project string) ([]string, error) {
	return ListProjectSecrets(m.DB, project)
}

func MigrateSecrets(db *gorm.DB) error {
	return db.AutoMigrate(&models.ProjectSecret{})
}

func SetProjectSecret(db *gorm.DB, project, key, value string) error {
	var s models.ProjectSecret
	if err := db.Where("project = ? AND key = ?", project, key).First(&s).Error; err == nil {
		s.Value = value
		return db.Save(&s).Error
	}
	s = models.ProjectSecret{Project: project, Key: key, Value: value}
	return db.Create(&s).Error
}

func GetProjectSecret(db *gorm.DB, project, key string) (string, error) {
	var s models.ProjectSecret
	if err := db.Where("project = ? AND key = ?", project, key).First(&s).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

func ListProjectSecrets(db *gorm.DB, project string) ([]string, error) {
	var secrets []models.ProjectSecret
	if err := db.Where("project = ?", project).Find(&secrets).Error; err != nil {
		return nil, err
	}
	keys := make([]string, len(secrets))
	for i, s := range secrets {
		keys[i] = s.Key
	}
	return keys, nil
}
