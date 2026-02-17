package models

import (
		"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type CliReq struct {
	State string `json:"state" bson:"state"`
}

type UserInput struct {
	Login       string `json:"login" bson:"login"`
	Name        string `json:"name" bson:"name"`
	Email       string `json:"email" bson:"email"`
	Company     string `json:"company" bson:"company"`
	URL         string `json:"url" bson:"URL"`
	GithubToken string `json:"githubToken" bson:"githubToken"`
	UserID      int64  `json:"userID" bson:"userID"`
	PersysToken string `json:"persysToken" bson:"persysToken"`
	State       string `json:"state" bson:"state"`
	Status      string `json:"status" bson:"status"`
	CreatedAt   string `json:"createdAt" bson:"createdAt"`
	UpdatedAt   string `json:"updatedAt" bson:"updatedAt"`
}

type DBResponse struct {
	Login       string `json:"login" bson:"login"`
	Name        string `json:"name" bson:"name"`
	Email       string `json:"email" bson:"email"`
	Company     string `json:"company" bson:"company"`
	URL         string `json:"url" bson:"URL"`
	GithubToken string `json:"githubToken" bson:"githubToken"`
	UserID      int64  `json:"userID" bson:"userID"`
	PersysToken string `json:"persysToken" bson:"persysToken"`
	State       string `json:"state" bson:"state"`
	Status      string `json:"status" bson:"status"`
	CreatedAt   string `json:"createdAt" bson:"createdAt"`
	UpdatedAt   string `json:"updatedAt" bson:"updatedAt"`
}

type UserResponse struct {
	ID        primitive.ObjectID `json:"id,omitempty" bson:"_id,omitempty"`
	Name      string             `json:"name,omitempty" bson:"name,omitempty"`
	Email     string             `json:"email,omitempty" bson:"email,omitempty"`
	Role      string             `json:"role,omitempty" bson:"role,omitempty"`
	CreatedAt time.Time          `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time          `json:"updated_at" bson:"updated_at"`
}
