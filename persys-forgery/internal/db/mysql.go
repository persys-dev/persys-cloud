package db

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
)

var DB *gorm.DB

func InitMySQL(dsn string) error {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return err
	}
	if err := db.AutoMigrate(&models.Project{}, &models.Job{}, &models.ProjectSecret{}); err != nil {
		return err
	}
	DB = db
	log.Println("MySQL connected and migrated.")
	return nil
}

func CreateProject(db *gorm.DB, p *models.Project) error {
	return db.Create(p).Error
}

func GetProject(db *gorm.DB, name string) (*models.Project, error) {
	var p models.Project
	if err := db.Where("name = ?", name).First(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func ListProjects(db *gorm.DB) ([]models.Project, error) {
	var projects []models.Project
	if err := db.Find(&projects).Error; err != nil {
		return nil, err
	}
	return projects, nil
}

func DeleteProject(db *gorm.DB, name string) error {
	return db.Where("name = ?", name).Delete(&models.Project{}).Error
}

func UpdateProject(db *gorm.DB, name string, updates map[string]interface{}) error {
	return db.Model(&models.Project{}).Where("name = ?", name).Updates(updates).Error
}
