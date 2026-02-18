package models

type ProjectSecret struct {
	ID      uint   `gorm:"primaryKey"`
	Project string `gorm:"index;not null"`
	Key     string `gorm:"not null"`
	Value   string `gorm:"not null"`
}
