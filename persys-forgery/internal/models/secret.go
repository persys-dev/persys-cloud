package models

type ProjectSecret struct {
	ID      uint   `gorm:"primaryKey"`
	Project string `gorm:"type:varchar(191);index;not null"`
	Key     string `gorm:"type:varchar(191);not null"`
	Value   string `gorm:"type:text;not null"`
}
